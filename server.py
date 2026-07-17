import json
import mimetypes
import os
import re
import sqlite3
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse


ROOT = Path(__file__).resolve().parent
DB_PATH = Path(os.environ.get("FINANCE_DB", ROOT / "finance.db")).resolve()
HOST = os.environ.get("HOST", "127.0.0.1")
PORT = int(os.environ.get("PORT", "3212"))

PUBLIC_FILES = {
    "/": "index.html",
    "/index.html": "index.html",
    "/styles.css": "styles.css",
    "/app.js": "app.js",
}
SECURITY_HEADERS = {
    "Content-Security-Policy": (
        "default-src 'self'; base-uri 'none'; connect-src 'self'; "
        "font-src 'self'; form-action 'self'; frame-ancestors 'none'; "
        "img-src 'self' data: blob:; object-src 'none'; "
        "script-src 'self'; style-src 'self' 'unsafe-inline'"
    ),
    "Cross-Origin-Opener-Policy": "same-origin",
    "Cross-Origin-Resource-Policy": "same-origin",
    "Permissions-Policy": "camera=(), geolocation=(), microphone=(), payment=()",
    "Referrer-Policy": "no-referrer",
    "X-Content-Type-Options": "nosniff",
    "X-Frame-Options": "DENY",
}

DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
MONTH_RE = re.compile(r"^\d{4}-\d{2}$")
HEX_RE = re.compile(r"^#[0-9a-fA-F]{6}$")
ID_RE = re.compile(r"^[a-zA-Z0-9_.:-]{1,96}$")
VALID_TYPES = {"expense", "income", "transfer"}
VALID_DEBT_DIRECTIONS = {"owe_me", "i_owe"}
VALID_ACCOUNT_KINDS = {"regular", "savings"}

DEFAULT_ACCOUNTS = [
    ("account-shared", "Общий", "#fb96ff", 1, 1, "regular", "", "Общий"),
    ("account-rina", "Котик Рина", "#173921", 2, 1, "regular", "", "Рина"),
    ("account-valera", "Котик Валера", "#82dde9", 3, 1, "regular", "", "Валера"),
    (
        "account-rina-savings",
        "Накопления Рина",
        "#6f8f61",
        4,
        0,
        "savings",
        "",
        "Рина",
    ),
    (
        "account-valera-savings",
        "Накопления Валера",
        "#557a95",
        5,
        0,
        "savings",
        "",
        "Валера",
    ),
]

DEFAULT_CATEGORIES = [
    ("expense-food", "expense", "Продукты", "#2f8f6f", 0, 1),
    ("expense-home", "expense", "Дом", "#5468ff", 0, 1),
    ("expense-transport", "expense", "Транспорт", "#e09f3e", 0, 1),
    ("expense-health", "expense", "Здоровье", "#d94f70", 0, 1),
    ("expense-leisure", "expense", "Отдых", "#7c5cce", 0, 1),
    ("expense-other", "expense", "Другое", "#6b7280", 0, 1),
    ("income-salary", "income", "Зарплата", "#1f9d55", 0, 1),
    ("income-projects", "income", "Проекты", "#2474a6", 0, 1),
    ("income-gifts", "income", "Подарки", "#b7791f", 0, 1),
    ("income-other", "income", "Другое", "#6b7280", 0, 1),
]


class ApiError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status
        self.message = message


def now_iso():
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


@contextmanager
def connect():
    connection = sqlite3.connect(DB_PATH)
    connection.row_factory = sqlite3.Row
    connection.execute("PRAGMA foreign_keys = ON")
    try:
        with connection:
            yield connection
    finally:
        connection.close()


def init_db():
    DB_PATH.parent.mkdir(parents=True, exist_ok=True)
    with connect() as con:
        con.executescript(
            """
            CREATE TABLE IF NOT EXISTS accounts (
                id TEXT PRIMARY KEY,
                name TEXT NOT NULL,
                color TEXT NOT NULL,
                sort_order INTEGER NOT NULL DEFAULT 0,
                system INTEGER NOT NULL DEFAULT 0,
                kind TEXT NOT NULL DEFAULT 'regular',
                bank TEXT NOT NULL DEFAULT '',
                owner TEXT NOT NULL DEFAULT '',
                created_at TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS categories (
                id TEXT PRIMARY KEY,
                type TEXT NOT NULL CHECK (type IN ('expense', 'income')),
                name TEXT NOT NULL,
                color TEXT NOT NULL,
                budget_cents INTEGER NOT NULL DEFAULT 0,
                system INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS transactions (
                id TEXT PRIMARY KEY,
                type TEXT NOT NULL CHECK (type IN ('expense', 'income', 'transfer')),
                account_id TEXT NOT NULL DEFAULT 'account-shared',
                to_account_id TEXT,
                category_id TEXT,
                amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
                date TEXT NOT NULL,
                note TEXT NOT NULL DEFAULT '',
                is_balance_adjustment INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL,
                FOREIGN KEY (account_id) REFERENCES accounts(id)
                    ON DELETE RESTRICT,
                FOREIGN KEY (to_account_id) REFERENCES accounts(id)
                    ON DELETE RESTRICT,
                FOREIGN KEY (category_id) REFERENCES categories(id)
                    ON DELETE RESTRICT,
                CHECK (type = 'transfer' OR category_id IS NOT NULL),
                CHECK (type <> 'transfer' OR (to_account_id IS NOT NULL AND to_account_id <> account_id))
            );

            CREATE INDEX IF NOT EXISTS idx_accounts_sort
                ON accounts(sort_order, name COLLATE NOCASE);
            CREATE INDEX IF NOT EXISTS idx_categories_type
                ON categories(type, created_at);
            CREATE INDEX IF NOT EXISTS idx_transactions_date
                ON transactions(date DESC);
            CREATE INDEX IF NOT EXISTS idx_transactions_type
                ON transactions(type);
            CREATE INDEX IF NOT EXISTS idx_transactions_category
                ON transactions(category_id);

            CREATE TABLE IF NOT EXISTS goals (
                id TEXT PRIMARY KEY,
                name TEXT NOT NULL,
                target_cents INTEGER NOT NULL CHECK (target_cents > 0),
                saved_cents INTEGER NOT NULL DEFAULT 0 CHECK (saved_cents >= 0),
                target_date TEXT NOT NULL DEFAULT '',
                color TEXT NOT NULL,
                archived INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS debts (
                id TEXT PRIMARY KEY,
                person TEXT NOT NULL,
                direction TEXT NOT NULL CHECK (direction IN ('owe_me', 'i_owe')),
                amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
                due_date TEXT NOT NULL DEFAULT '',
                note TEXT NOT NULL DEFAULT '',
                archived INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS debt_payments (
                id TEXT PRIMARY KEY,
                debt_id TEXT NOT NULL,
                amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
                date TEXT NOT NULL,
                note TEXT NOT NULL DEFAULT '',
                created_at TEXT NOT NULL,
                FOREIGN KEY (debt_id) REFERENCES debts(id)
                    ON DELETE CASCADE
            );

            CREATE INDEX IF NOT EXISTS idx_goals_archived
                ON goals(archived, created_at);
            CREATE INDEX IF NOT EXISTS idx_debts_archived
                ON debts(archived, created_at);
            CREATE INDEX IF NOT EXISTS idx_debt_payments_debt
                ON debt_payments(debt_id, date);
            """
        )

        accounts_migrated = ensure_accounts_schema(con)
        account_count = con.execute("SELECT COUNT(*) AS total FROM accounts").fetchone()[
            "total"
        ]
        if account_count == 0:
            created_at = now_iso()
            con.executemany(
                """
                INSERT INTO accounts
                    (id, name, color, sort_order, system, kind, bank, owner, created_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                [(*account, created_at) for account in DEFAULT_ACCOUNTS],
            )
        elif accounts_migrated:
            created_at = now_iso()
            con.executemany(
                """
                INSERT OR IGNORE INTO accounts
                    (id, name, color, sort_order, system, kind, bank, owner, created_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                [(*account, created_at) for account in DEFAULT_ACCOUNTS[-2:]],
            )

        ensure_transactions_schema(con)
        con.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_transactions_date
            ON transactions(date DESC)
            """
        )
        con.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_transactions_type
            ON transactions(type)
            """
        )
        con.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_transactions_category
            ON transactions(category_id)
            """
        )
        con.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_transactions_account
            ON transactions(account_id)
            """
        )
        con.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_transactions_to_account
            ON transactions(to_account_id)
            """
        )

        count = con.execute("SELECT COUNT(*) AS total FROM categories").fetchone()["total"]
        if count == 0:
            created_at = now_iso()
            con.executemany(
                """
                INSERT INTO categories
                    (id, type, name, color, budget_cents, system, created_at)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                """,
                [(*category, created_at) for category in DEFAULT_CATEGORIES],
            )


def ensure_accounts_schema(con):
    columns = {
        row["name"] for row in con.execute("PRAGMA table_info(accounts)").fetchall()
    }
    migrated = False
    additions = (
        ("kind", "TEXT NOT NULL DEFAULT 'regular'"),
        ("bank", "TEXT NOT NULL DEFAULT ''"),
        ("owner", "TEXT NOT NULL DEFAULT ''"),
    )
    for name, definition in additions:
        if name not in columns:
            con.execute(f"ALTER TABLE accounts ADD COLUMN {name} {definition}")
            migrated = True
    if migrated:
        con.executemany(
            "UPDATE accounts SET owner = ? WHERE id = ? AND owner = ''",
            (
                ("Общий", "account-shared"),
                ("Рина", "account-rina"),
                ("Валера", "account-valera"),
            ),
        )
    return migrated


def ensure_transactions_schema(con):
    columns = {
        row["name"]: row for row in con.execute("PRAGMA table_info(transactions)").fetchall()
    }
    table = con.execute(
        "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'transactions'"
    ).fetchone()
    sql = table["sql"] or ""
    needs_rebuild = (
        "to_account_id" not in columns
        or "'transfer'" not in sql
        or (columns.get("category_id") is not None and columns["category_id"]["notnull"])
    )
    if not needs_rebuild:
        if "is_balance_adjustment" not in columns:
            con.execute(
                "ALTER TABLE transactions ADD COLUMN "
                "is_balance_adjustment INTEGER NOT NULL DEFAULT 0"
            )
            con.execute(
                """
                UPDATE transactions
                SET is_balance_adjustment = 1
                WHERE note = 'Коррекция баланса'
                """
            )
        return

    con.execute("ALTER TABLE transactions RENAME TO transactions_old")
    con.execute(
        """
        CREATE TABLE transactions (
            id TEXT PRIMARY KEY,
            type TEXT NOT NULL CHECK (type IN ('expense', 'income', 'transfer')),
            account_id TEXT NOT NULL DEFAULT 'account-shared',
            to_account_id TEXT,
            category_id TEXT,
            amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
            date TEXT NOT NULL,
            note TEXT NOT NULL DEFAULT '',
            is_balance_adjustment INTEGER NOT NULL DEFAULT 0,
            created_at TEXT NOT NULL,
            FOREIGN KEY (account_id) REFERENCES accounts(id)
                ON DELETE RESTRICT,
            FOREIGN KEY (to_account_id) REFERENCES accounts(id)
                ON DELETE RESTRICT,
            FOREIGN KEY (category_id) REFERENCES categories(id)
                ON DELETE RESTRICT,
            CHECK (type = 'transfer' OR category_id IS NOT NULL),
            CHECK (type <> 'transfer' OR (to_account_id IS NOT NULL AND to_account_id <> account_id))
        )
        """
    )
    old_columns = {
        row["name"] for row in con.execute("PRAGMA table_info(transactions_old)").fetchall()
    }
    to_account_select = "to_account_id" if "to_account_id" in old_columns else "NULL"
    adjustment_select = (
        "is_balance_adjustment" if "is_balance_adjustment" in old_columns else "0"
    )
    con.execute(
        f"""
        INSERT INTO transactions
            (id, type, account_id, to_account_id, category_id, amount_cents, date, note,
             is_balance_adjustment, created_at)
        SELECT
            id,
            CASE WHEN type IN ('expense', 'income', 'transfer') THEN type ELSE 'expense' END,
            COALESCE(account_id, 'account-shared'),
            {to_account_select},
            category_id,
            amount_cents,
            date,
            note,
            {adjustment_select},
            created_at
        FROM transactions_old
        WHERE type IN ('expense', 'income') OR ({to_account_select} IS NOT NULL)
        """
    )
    if "is_balance_adjustment" not in old_columns:
        con.execute(
            """
            UPDATE transactions
            SET is_balance_adjustment = 1
            WHERE note = 'Коррекция баланса'
            """
        )
    con.execute("DROP TABLE transactions_old")


def account_to_json(row):
    return {
        "id": row["id"],
        "name": row["name"],
        "color": row["color"],
        "sortOrder": row["sort_order"],
        "system": bool(row["system"]),
        "kind": row["kind"],
        "bank": row["bank"],
        "owner": row["owner"],
        "createdAt": row["created_at"],
    }


def category_to_json(row):
    return {
        "id": row["id"],
        "type": row["type"],
        "name": row["name"],
        "color": row["color"],
        "budgetCents": row["budget_cents"],
        "system": bool(row["system"]),
        "createdAt": row["created_at"],
    }


def transaction_to_json(row):
    return {
        "id": row["id"],
        "type": row["type"],
        "accountId": row["account_id"],
        "toAccountId": row["to_account_id"],
        "categoryId": row["category_id"],
        "amountCents": row["amount_cents"],
        "date": row["date"],
        "note": row["note"],
        "isBalanceAdjustment": bool(row["is_balance_adjustment"]),
        "createdAt": row["created_at"],
    }


def goal_to_json(row):
    return {
        "id": row["id"],
        "name": row["name"],
        "targetCents": row["target_cents"],
        "savedCents": row["saved_cents"],
        "targetDate": row["target_date"],
        "color": row["color"],
        "archived": bool(row["archived"]),
        "createdAt": row["created_at"],
    }


def debt_to_json(row):
    return {
        "id": row["id"],
        "person": row["person"],
        "direction": row["direction"],
        "amountCents": row["amount_cents"],
        "paidCents": row["paid_cents"],
        "leftCents": max(row["amount_cents"] - row["paid_cents"], 0),
        "dueDate": row["due_date"],
        "note": row["note"],
        "archived": bool(row["archived"]),
        "createdAt": row["created_at"],
    }


def debt_payment_to_json(row):
    return {
        "id": row["id"],
        "debtId": row["debt_id"],
        "amountCents": row["amount_cents"],
        "date": row["date"],
        "note": row["note"],
        "createdAt": row["created_at"],
    }


def make_id(prefix):
    return f"{prefix}-{uuid.uuid4().hex[:16]}"


def clean_string(value, field, max_length):
    if not isinstance(value, str):
        raise ApiError(400, f"{field}: ожидается строка")
    value = value.strip()
    if not value:
        raise ApiError(400, f"{field}: поле не может быть пустым")
    if len(value) > max_length:
        raise ApiError(400, f"{field}: слишком длинное значение")
    return value


def clean_optional_string(value, field, max_length):
    if value is None:
        return ""
    if not isinstance(value, str):
        raise ApiError(400, f"{field}: ожидается строка")
    return value.strip()[:max_length]


def clean_id(value, field="id"):
    value = clean_string(value, field, 96)
    if not ID_RE.match(value):
        raise ApiError(400, f"{field}: неверный формат")
    return value


def clean_type(value):
    if value not in VALID_TYPES:
        raise ApiError(400, "type: допустимы expense, income или transfer")
    return value


def clean_debt_direction(value):
    if value not in VALID_DEBT_DIRECTIONS:
        raise ApiError(400, "direction: допустимы owe_me или i_owe")
    return value


def clean_account_kind(value):
    if value not in VALID_ACCOUNT_KINDS:
        raise ApiError(400, "kind: допустимы regular или savings")
    return value


def clean_color(value):
    value = clean_string(value, "color", 7)
    if not HEX_RE.match(value):
        raise ApiError(400, "color: нужен HEX-цвет в формате #1a2b3c")
    return value.lower()


def clean_date(value):
    value = clean_string(value, "date", 10)
    if not DATE_RE.match(value):
        raise ApiError(400, "date: нужен формат YYYY-MM-DD")
    try:
        datetime.strptime(value, "%Y-%m-%d")
    except ValueError as exc:
        raise ApiError(400, "date: некорректная дата") from exc
    return value


def clean_optional_date(value, field):
    if value is None or value == "":
        return ""
    value = clean_string(value, field, 10)
    if not DATE_RE.match(value):
        raise ApiError(400, f"{field}: нужен формат YYYY-MM-DD")
    try:
        datetime.strptime(value, "%Y-%m-%d")
    except ValueError as exc:
        raise ApiError(400, f"{field}: некорректная дата") from exc
    return value


def clean_cents(value, field, allow_zero=False):
    if isinstance(value, bool):
        raise ApiError(400, f"{field}: неверная сумма")
    try:
        cents = int(value)
    except (TypeError, ValueError) as exc:
        raise ApiError(400, f"{field}: неверная сумма") from exc
    minimum = 0 if allow_zero else 1
    if cents < minimum:
        raise ApiError(400, f"{field}: сумма должна быть больше нуля")
    if cents > 99_999_999_999:
        raise ApiError(400, f"{field}: сумма слишком большая")
    return cents


def clean_balance_cents(value, field):
    if isinstance(value, bool):
        raise ApiError(400, f"{field}: неверная сумма")
    try:
        cents = int(value)
    except (TypeError, ValueError) as exc:
        raise ApiError(400, f"{field}: неверная сумма") from exc
    if abs(cents) > 99_999_999_999:
        raise ApiError(400, f"{field}: сумма слишком большая")
    return cents


def normalize_category(payload, allow_id=False):
    category = {
        "id": clean_id(payload.get("id")) if allow_id else make_id("cat"),
        "type": clean_type(payload.get("type")),
        "name": clean_string(payload.get("name"), "name", 36),
        "color": clean_color(payload.get("color", "#6b7280")),
        "budget_cents": clean_cents(
            payload.get("budgetCents", 0), "budgetCents", allow_zero=True
        ),
        "system": 1 if bool(payload.get("system", False)) else 0,
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }
    if category["type"] == "income":
        category["budget_cents"] = 0
    return category


def normalize_account(payload, allow_id=False):
    return {
        "id": clean_id(payload.get("id")) if allow_id else make_id("account"),
        "name": clean_string(payload.get("name"), "name", 48),
        "color": clean_color(payload.get("color", "#6b7280")),
        "sort_order": clean_cents(
            payload.get("sortOrder", 0), "sortOrder", allow_zero=True
        ),
        "system": 1 if bool(payload.get("system", False)) else 0,
        "kind": clean_account_kind(payload.get("kind", "regular")),
        "bank": clean_optional_string(payload.get("bank"), "bank", 48),
        "owner": clean_optional_string(payload.get("owner"), "owner", 48),
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }


def normalize_transaction(
    payload, category_types=None, account_ids=None, allow_id=False
):
    tx_type = clean_type(payload.get("type"))
    category_id = None
    if tx_type != "transfer":
        category_id = clean_id(payload.get("categoryId"), "categoryId")
    to_account_id = None
    if tx_type == "transfer":
        to_account_id = clean_id(payload.get("toAccountId"), "toAccountId")
    transaction = {
        "id": clean_id(payload.get("id")) if allow_id else make_id("tx"),
        "type": tx_type,
        "account_id": clean_id(payload.get("accountId", "account-shared"), "accountId"),
        "to_account_id": to_account_id,
        "category_id": category_id,
        "amount_cents": clean_cents(payload.get("amountCents"), "amountCents"),
        "date": clean_date(payload.get("date")),
        "note": clean_optional_string(payload.get("note"), "note", 160),
        "is_balance_adjustment": 1
        if bool(payload.get("isBalanceAdjustment", False))
        else 0,
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }
    if tx_type == "transfer" and transaction["to_account_id"] == transaction["account_id"]:
        raise ApiError(400, "toAccountId: выберите другой счет")
    if category_types is not None and tx_type != "transfer":
        category_type = category_types.get(transaction["category_id"])
        if category_type is None:
            raise ApiError(400, "categoryId: категория не найдена")
        if category_type != transaction["type"]:
            raise ApiError(400, "categoryId: тип категории не совпадает с операцией")
    if account_ids is not None and transaction["account_id"] not in account_ids:
        raise ApiError(400, "accountId: счет не найден")
    if (
        account_ids is not None
        and transaction["to_account_id"] is not None
        and transaction["to_account_id"] not in account_ids
    ):
        raise ApiError(400, "toAccountId: счет не найден")
    return transaction


def normalize_goal(payload, allow_id=False):
    return {
        "id": clean_id(payload.get("id")) if allow_id else make_id("goal"),
        "name": clean_string(payload.get("name"), "name", 48),
        "target_cents": clean_cents(payload.get("targetCents"), "targetCents"),
        "saved_cents": clean_cents(payload.get("savedCents", 0), "savedCents", allow_zero=True),
        "target_date": clean_optional_date(payload.get("targetDate"), "targetDate"),
        "color": clean_color(payload.get("color", "#5d704d")),
        "archived": 1 if bool(payload.get("archived", False)) else 0,
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }


def normalize_debt(payload, allow_id=False):
    return {
        "id": clean_id(payload.get("id")) if allow_id else make_id("debt"),
        "person": clean_string(payload.get("person"), "person", 48),
        "direction": clean_debt_direction(payload.get("direction")),
        "amount_cents": clean_cents(payload.get("amountCents"), "amountCents"),
        "due_date": clean_optional_date(payload.get("dueDate"), "dueDate"),
        "note": clean_optional_string(payload.get("note"), "note", 160),
        "archived": 1 if bool(payload.get("archived", False)) else 0,
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }


def normalize_debt_payment(payload, debt_ids=None, allow_id=False):
    payment = {
        "id": clean_id(payload.get("id")) if allow_id else make_id("pay"),
        "debt_id": clean_id(payload.get("debtId"), "debtId"),
        "amount_cents": clean_cents(payload.get("amountCents"), "amountCents"),
        "date": clean_date(payload.get("date")),
        "note": clean_optional_string(payload.get("note"), "note", 160),
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }
    if debt_ids is not None and payment["debt_id"] not in debt_ids:
        raise ApiError(400, "debtId: долг не найден")
    return payment


def split_shared_expense(transaction):
    if transaction["type"] != "expense" or transaction["account_id"] != "account-shared":
        return None
    if transaction["amount_cents"] < 2:
        raise ApiError(400, "amountCents: общий расход должен быть минимум 2 копейки")

    first_half = transaction["amount_cents"] // 2
    second_half = transaction["amount_cents"] - first_half
    base_note = transaction["note"] or "Общий расход"
    created_at = transaction["created_at"]
    shared = {
        **transaction,
        "id": make_id("tx"),
        "account_id": "account-rina",
        "amount_cents": first_half,
        "note": f"{base_note} · 50% общего",
        "created_at": created_at,
    }
    counterpart = {
        **transaction,
        "id": make_id("tx"),
        "account_id": "account-valera",
        "amount_cents": second_half,
        "note": f"{base_note} · 50% общего",
        "created_at": created_at,
    }
    return [shared, counterpart]


def fallback_category(con, tx_type):
    return con.execute(
        """
        SELECT id FROM categories
        WHERE type = ?
        ORDER BY CASE WHEN name = 'Другое' THEN 0 ELSE 1 END, created_at
        LIMIT 1
        """,
        (tx_type,),
    ).fetchone()


def account_balance_through_date(con, account_id, date):
    rows = con.execute(
        """
        SELECT type, account_id, to_account_id, amount_cents
        FROM transactions
        WHERE date <= ?
          AND (account_id = ? OR to_account_id = ?)
        """,
        (date, account_id, account_id),
    ).fetchall()
    balance = 0
    for row in rows:
        if row["type"] == "income" and row["account_id"] == account_id:
            balance += row["amount_cents"]
        elif row["type"] == "expense" and row["account_id"] == account_id:
            balance -= row["amount_cents"]
        elif row["type"] == "transfer":
            if row["account_id"] == account_id:
                balance -= row["amount_cents"]
            if row["to_account_id"] == account_id:
                balance += row["amount_cents"]
    return balance


class FinanceHandler(BaseHTTPRequestHandler):
    server_version = "PersonalFinance/1.0"

    def end_headers(self):
        for key, value in SECURITY_HEADERS.items():
            self.send_header(key, value)
        super().end_headers()

    def do_GET(self):
        self.route("GET")

    def do_POST(self):
        self.route("POST")

    def do_PATCH(self):
        self.route("PATCH")

    def do_DELETE(self):
        self.route("DELETE")

    def route(self, method):
        parsed = urlparse(self.path)
        path = parsed.path
        try:
            if path.startswith("/api/"):
                self.route_api(method, path, parse_qs(parsed.query))
                return
            if method != "GET":
                raise ApiError(405, "Метод не поддерживается")
            self.serve_static(path)
        except ApiError as exc:
            self.send_json(exc.status, {"error": exc.message})
        except Exception as exc:
            self.log_error("Unhandled error: %s", exc)
            self.send_json(500, {"error": "Внутренняя ошибка сервера"})

    def route_api(self, method, path, query):
        if method == "GET" and path == "/api/health":
            with connect() as con:
                con.execute("SELECT 1").fetchone()
            self.send_json(200, {"ok": True})
            return
        if method == "GET" and path == "/api/categories":
            self.get_categories()
            return
        if method == "GET" and path == "/api/accounts":
            self.get_accounts()
            return
        if method == "POST" and path == "/api/accounts":
            self.create_account()
            return
        if method == "PATCH" and path.startswith("/api/accounts/"):
            self.update_account(path.rsplit("/", 1)[-1])
            return
        if method == "DELETE" and path.startswith("/api/accounts/"):
            self.delete_account(path.rsplit("/", 1)[-1])
            return
        if method == "POST" and path == "/api/categories":
            self.create_category()
            return
        if method == "PATCH" and path.startswith("/api/categories/"):
            self.update_category(path.rsplit("/", 1)[-1])
            return
        if method == "GET" and path == "/api/transactions":
            self.get_transactions(query)
            return
        if method == "POST" and path == "/api/transactions":
            self.create_transaction()
            return
        if method == "GET" and path == "/api/goals":
            self.get_goals()
            return
        if method == "POST" and path == "/api/goals":
            self.create_goal()
            return
        if method == "PATCH" and path.startswith("/api/goals/"):
            self.update_goal(path.rsplit("/", 1)[-1])
            return
        if method == "DELETE" and path.startswith("/api/goals/"):
            self.delete_goal(path.rsplit("/", 1)[-1])
            return
        if method == "GET" and path == "/api/debts":
            self.get_debts()
            return
        if method == "POST" and path == "/api/debts":
            self.create_debt()
            return
        if method == "PATCH" and path.startswith("/api/debts/"):
            self.update_debt(path.rsplit("/", 1)[-1])
            return
        if method == "DELETE" and path.startswith("/api/debts/"):
            self.delete_debt(path.rsplit("/", 1)[-1])
            return
        if method == "POST" and path == "/api/debt-payments":
            self.create_debt_payment()
            return
        if method == "DELETE" and path.startswith("/api/debt-payments/"):
            self.delete_debt_payment(path.rsplit("/", 1)[-1])
            return
        if method == "POST" and path == "/api/balance-adjustment":
            self.create_balance_adjustment()
            return
        if method == "PATCH" and path.startswith("/api/transactions/"):
            self.update_transaction(path.rsplit("/", 1)[-1])
            return
        if method == "GET" and path == "/api/export":
            self.export_data()
            return
        if method == "POST" and path == "/api/import":
            self.import_data()
            return
        if method == "DELETE" and path.startswith("/api/categories/"):
            self.delete_category(path.rsplit("/", 1)[-1])
            return
        if method == "DELETE" and path.startswith("/api/transactions/"):
            self.delete_transaction(path.rsplit("/", 1)[-1])
            return
        raise ApiError(404, "Маршрут не найден")

    def read_json(self):
        length = int(self.headers.get("Content-Length", "0") or "0")
        if length <= 0:
            return {}
        if length > 2_000_000:
            raise ApiError(413, "Слишком большой запрос")
        raw = self.rfile.read(length)
        try:
            return json.loads(raw.decode("utf-8"))
        except json.JSONDecodeError as exc:
            raise ApiError(400, "Некорректный JSON") from exc

    def send_json(self, status, payload, extra_headers=None):
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        if extra_headers:
            for key, value in extra_headers.items():
                self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def serve_static(self, path):
        public_path = unquote(path)
        filename = PUBLIC_FILES.get(public_path)
        if filename is None:
            raise ApiError(404, "Файл не найден")

        target = ROOT / filename
        if not target.is_file():
            raise ApiError(404, "Файл не найден")

        content = target.read_bytes()
        content_type = mimetypes.guess_type(str(target))[0] or "application/octet-stream"
        if target.suffix in {".html", ".css", ".js"}:
            content_type += "; charset=utf-8"
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(content)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(content)

    def get_accounts(self):
        with connect() as con:
            rows = con.execute(
                """
                SELECT * FROM accounts
                ORDER BY sort_order, name COLLATE NOCASE
                """
            ).fetchall()
        self.send_json(200, {"accounts": [account_to_json(row) for row in rows]})

    def create_account(self):
        payload = self.read_json()
        with connect() as con:
            next_order = con.execute(
                "SELECT COALESCE(MAX(sort_order), 0) + 1 AS value FROM accounts"
            ).fetchone()["value"]
            account = normalize_account(
                {**payload, "sortOrder": next_order, "system": False}
            )
            duplicate = con.execute(
                """
                SELECT id FROM accounts
                WHERE lower(name) = lower(?) AND lower(bank) = lower(?)
                """,
                (account["name"], account["bank"]),
            ).fetchone()
            if duplicate:
                raise ApiError(409, "Такой счет в этом банке уже есть")
            con.execute(
                """
                INSERT INTO accounts
                    (id, name, color, sort_order, system, kind, bank, owner, created_at)
                VALUES
                    (:id, :name, :color, :sort_order, :system, :kind, :bank, :owner,
                     :created_at)
                """,
                account,
            )
            row = con.execute(
                "SELECT * FROM accounts WHERE id = ?", (account["id"],)
            ).fetchone()
        self.send_json(201, {"account": account_to_json(row)})

    def update_account(self, account_id):
        account_id = clean_id(account_id)
        payload = self.read_json()
        with connect() as con:
            existing = con.execute(
                "SELECT * FROM accounts WHERE id = ?", (account_id,)
            ).fetchone()
            if existing is None:
                raise ApiError(404, "Счет не найден")
            account = normalize_account(
                {
                    "id": account_id,
                    "name": payload.get("name", existing["name"]),
                    "color": payload.get("color", existing["color"]),
                    "sortOrder": payload.get("sortOrder", existing["sort_order"]),
                    "system": bool(existing["system"]),
                    "kind": payload.get("kind", existing["kind"]),
                    "bank": payload.get("bank", existing["bank"]),
                    "owner": payload.get("owner", existing["owner"]),
                    "createdAt": existing["created_at"],
                },
                allow_id=True,
            )
            duplicate = con.execute(
                """
                SELECT id FROM accounts
                WHERE lower(name) = lower(?) AND lower(bank) = lower(?) AND id <> ?
                """,
                (account["name"], account["bank"], account_id),
            ).fetchone()
            if duplicate:
                raise ApiError(409, "Такой счет в этом банке уже есть")
            con.execute(
                """
                UPDATE accounts
                SET name = :name,
                    color = :color,
                    sort_order = :sort_order,
                    kind = :kind,
                    bank = :bank,
                    owner = :owner
                WHERE id = :id
                """,
                account,
            )
            row = con.execute(
                "SELECT * FROM accounts WHERE id = ?", (account_id,)
            ).fetchone()
        self.send_json(200, {"account": account_to_json(row)})

    def delete_account(self, account_id):
        account_id = clean_id(account_id)
        with connect() as con:
            account = con.execute(
                "SELECT * FROM accounts WHERE id = ?", (account_id,)
            ).fetchone()
            if account is None:
                raise ApiError(404, "Счет не найден")
            if account["system"]:
                raise ApiError(409, "Системный счет можно редактировать, но нельзя удалить")
            used_count = con.execute(
                """
                SELECT COUNT(*) AS total FROM transactions
                WHERE account_id = ? OR to_account_id = ?
                """,
                (account_id, account_id),
            ).fetchone()["total"]
            if used_count:
                raise ApiError(409, "Сначала удалите или перенесите операции этого счета")
            con.execute("DELETE FROM accounts WHERE id = ?", (account_id,))
        self.send_json(200, {"ok": True})

    def get_categories(self):
        with connect() as con:
            rows = con.execute(
                """
                SELECT * FROM categories
                ORDER BY type DESC, system DESC, name COLLATE NOCASE
                """
            ).fetchall()
        self.send_json(200, {"categories": [category_to_json(row) for row in rows]})

    def create_category(self):
        payload = self.read_json()
        category = normalize_category(payload)
        with connect() as con:
            duplicate = con.execute(
                """
                SELECT id FROM categories
                WHERE type = ? AND lower(name) = lower(?)
                """,
                (category["type"], category["name"]),
            ).fetchone()
            if duplicate:
                raise ApiError(409, "Такая категория уже есть")
            con.execute(
                """
                INSERT INTO categories
                    (id, type, name, color, budget_cents, system, created_at)
                VALUES (:id, :type, :name, :color, :budget_cents, :system, :created_at)
                """,
                category,
            )
            row = con.execute(
                "SELECT * FROM categories WHERE id = ?", (category["id"],)
            ).fetchone()
        self.send_json(201, {"category": category_to_json(row)})

    def update_category(self, category_id):
        category_id = clean_id(category_id)
        payload = self.read_json()
        category = normalize_category({**payload, "id": category_id}, allow_id=True)
        with connect() as con:
            existing = con.execute(
                "SELECT * FROM categories WHERE id = ?", (category_id,)
            ).fetchone()
            if existing is None:
                raise ApiError(404, "Категория не найдена")
            duplicate = con.execute(
                """
                SELECT id FROM categories
                WHERE type = ? AND lower(name) = lower(?) AND id <> ?
                """,
                (category["type"], category["name"], category_id),
            ).fetchone()
            if duplicate:
                raise ApiError(409, "Такая категория уже есть")
            con.execute(
                """
                UPDATE categories
                SET type = :type,
                    name = :name,
                    color = :color,
                    budget_cents = :budget_cents
                WHERE id = :id
                """,
                category,
            )
            if existing["type"] != category["type"]:
                fallback = con.execute(
                    """
                    SELECT id FROM categories
                    WHERE type = ?
                    ORDER BY CASE WHEN name = 'Другое' THEN 0 ELSE 1 END, created_at
                    LIMIT 1
                    """,
                    (existing["type"],),
                ).fetchone()
                used_count = con.execute(
                    "SELECT COUNT(*) AS total FROM transactions WHERE category_id = ?",
                    (category_id,),
                ).fetchone()["total"]
                if used_count and fallback is None:
                    raise ApiError(409, "Нужна еще одна категория старого типа для переноса операций")
                if fallback:
                    con.execute(
                        "UPDATE transactions SET category_id = ? WHERE category_id = ?",
                        (fallback["id"], category_id),
                    )
            row = con.execute("SELECT * FROM categories WHERE id = ?", (category_id,)).fetchone()
        self.send_json(200, {"category": category_to_json(row)})

    def delete_category(self, category_id):
        category_id = clean_id(category_id)
        with connect() as con:
            category = con.execute(
                "SELECT * FROM categories WHERE id = ?", (category_id,)
            ).fetchone()
            if category is None:
                raise ApiError(404, "Категория не найдена")
            same_type_count = con.execute(
                "SELECT COUNT(*) AS total FROM categories WHERE type = ?",
                (category["type"],),
            ).fetchone()["total"]
            if same_type_count <= 1:
                raise ApiError(409, "Нельзя удалить последнюю категорию этого типа")
            fallback = con.execute(
                """
                SELECT id FROM categories
                WHERE type = ? AND id <> ?
                ORDER BY CASE WHEN name = 'Другое' THEN 0 ELSE 1 END, created_at
                LIMIT 1
                """,
                (category["type"], category_id),
            ).fetchone()
            con.execute(
                "UPDATE transactions SET category_id = ? WHERE category_id = ?",
                (fallback["id"], category_id),
            )
            con.execute("DELETE FROM categories WHERE id = ?", (category_id,))
        self.send_json(200, {"ok": True})

    def get_transactions(self, query):
        clauses = []
        params = []

        month = (query.get("month") or [""])[0]
        if month:
            if not MONTH_RE.match(month):
                raise ApiError(400, "month: нужен формат YYYY-MM")
            clauses.append("date LIKE ?")
            params.append(f"{month}-%")

        tx_type = (query.get("type") or [""])[0]
        if tx_type:
            clean_type(tx_type)
            clauses.append("type = ?")
            params.append(tx_type)

        category_id = (query.get("category") or [""])[0]
        if category_id:
            clauses.append("category_id = ?")
            params.append(clean_id(category_id, "category"))

        account_id = (query.get("account") or [""])[0]
        if account_id and account_id != "all":
            cleaned_account = clean_id(account_id, "account")
            clauses.append("(account_id = ? OR to_account_id = ?)")
            params.extend([cleaned_account, cleaned_account])

        search = (query.get("q") or [""])[0].strip()
        if search:
            clauses.append("note LIKE ?")
            params.append(f"%{search[:80]}%")

        where = f"WHERE {' AND '.join(clauses)}" if clauses else ""
        with connect() as con:
            rows = con.execute(
                f"""
                SELECT * FROM transactions
                {where}
                ORDER BY date DESC, created_at DESC
                """,
                params,
            ).fetchall()
        self.send_json(200, {"transactions": [transaction_to_json(row) for row in rows]})

    def create_transaction(self):
        payload = self.read_json()
        with connect() as con:
            categories = con.execute("SELECT id, type FROM categories").fetchall()
            category_types = {row["id"]: row["type"] for row in categories}
            accounts = con.execute("SELECT id FROM accounts").fetchall()
            account_ids = {row["id"] for row in accounts}
            transaction = normalize_transaction(
                {**payload, "isBalanceAdjustment": False},
                category_types,
                account_ids,
            )
            if (
                transaction["type"] == "expense"
                and transaction["account_id"] == "account-shared"
                and not {"account-rina", "account-valera"}.issubset(account_ids)
            ):
                raise ApiError(409, "Для общего расхода нужны счета Котик Рина и Котик Валера")
            transactions = split_shared_expense(transaction) or [transaction]
            con.executemany(
                """
                INSERT INTO transactions
                    (id, type, account_id, to_account_id, category_id, amount_cents, date, note, created_at)
                VALUES (:id, :type, :account_id, :to_account_id, :category_id, :amount_cents, :date, :note, :created_at)
                """,
                transactions,
            )
            placeholders = ",".join("?" for _ in transactions)
            rows = con.execute(
                f"SELECT * FROM transactions WHERE id IN ({placeholders})",
                [item["id"] for item in transactions],
            ).fetchall()
        payload_key = "transactions" if len(rows) > 1 else "transaction"
        payload_value = [transaction_to_json(row) for row in rows]
        self.send_json(201, {payload_key: payload_value if len(rows) > 1 else payload_value[0]})

    def get_goals(self):
        with connect() as con:
            rows = con.execute(
                """
                SELECT * FROM goals
                ORDER BY archived, created_at DESC
                """
            ).fetchall()
        self.send_json(200, {"goals": [goal_to_json(row) for row in rows]})

    def create_goal(self):
        payload = self.read_json()
        goal = normalize_goal(payload)
        with connect() as con:
            con.execute(
                """
                INSERT INTO goals
                    (id, name, target_cents, saved_cents, target_date, color, archived, created_at)
                VALUES
                    (:id, :name, :target_cents, :saved_cents, :target_date, :color, :archived, :created_at)
                """,
                goal,
            )
            row = con.execute("SELECT * FROM goals WHERE id = ?", (goal["id"],)).fetchone()
        self.send_json(201, {"goal": goal_to_json(row)})

    def update_goal(self, goal_id):
        goal_id = clean_id(goal_id)
        payload = self.read_json()
        with connect() as con:
            existing = con.execute("SELECT * FROM goals WHERE id = ?", (goal_id,)).fetchone()
            if existing is None:
                raise ApiError(404, "Цель не найдена")
            goal = normalize_goal(
                {
                    "id": goal_id,
                    "name": payload.get("name", existing["name"]),
                    "targetCents": payload.get("targetCents", existing["target_cents"]),
                    "savedCents": payload.get("savedCents", existing["saved_cents"]),
                    "targetDate": payload.get("targetDate", existing["target_date"]),
                    "color": payload.get("color", existing["color"]),
                    "archived": payload.get("archived", bool(existing["archived"])),
                    "createdAt": existing["created_at"],
                },
                allow_id=True,
            )
            con.execute(
                """
                UPDATE goals
                SET name = :name,
                    target_cents = :target_cents,
                    saved_cents = :saved_cents,
                    target_date = :target_date,
                    color = :color,
                    archived = :archived
                WHERE id = :id
                """,
                goal,
            )
            row = con.execute("SELECT * FROM goals WHERE id = ?", (goal_id,)).fetchone()
        self.send_json(200, {"goal": goal_to_json(row)})

    def delete_goal(self, goal_id):
        goal_id = clean_id(goal_id)
        with connect() as con:
            cursor = con.execute("DELETE FROM goals WHERE id = ?", (goal_id,))
            if cursor.rowcount == 0:
                raise ApiError(404, "Цель не найдена")
        self.send_json(200, {"ok": True})

    def get_debts(self):
        with connect() as con:
            debts = con.execute(
                """
                SELECT d.*,
                       COALESCE(SUM(p.amount_cents), 0) AS paid_cents
                FROM debts d
                LEFT JOIN debt_payments p ON p.debt_id = d.id
                GROUP BY d.id
                ORDER BY d.archived, d.created_at DESC
                """
            ).fetchall()
            payments = con.execute(
                """
                SELECT * FROM debt_payments
                ORDER BY date DESC, created_at DESC
                """
            ).fetchall()
        self.send_json(
            200,
            {
                "debts": [debt_to_json(row) for row in debts],
                "payments": [debt_payment_to_json(row) for row in payments],
            },
        )

    def create_debt(self):
        payload = self.read_json()
        debt = normalize_debt(payload)
        with connect() as con:
            con.execute(
                """
                INSERT INTO debts
                    (id, person, direction, amount_cents, due_date, note, archived, created_at)
                VALUES
                    (:id, :person, :direction, :amount_cents, :due_date, :note, :archived, :created_at)
                """,
                debt,
            )
            row = con.execute(
                """
                SELECT d.*, 0 AS paid_cents
                FROM debts d
                WHERE d.id = ?
                """,
                (debt["id"],),
            ).fetchone()
        self.send_json(201, {"debt": debt_to_json(row)})

    def update_debt(self, debt_id):
        debt_id = clean_id(debt_id)
        payload = self.read_json()
        with connect() as con:
            existing = con.execute("SELECT * FROM debts WHERE id = ?", (debt_id,)).fetchone()
            if existing is None:
                raise ApiError(404, "Долг не найден")
            debt = normalize_debt(
                {
                    "id": debt_id,
                    "person": payload.get("person", existing["person"]),
                    "direction": payload.get("direction", existing["direction"]),
                    "amountCents": payload.get("amountCents", existing["amount_cents"]),
                    "dueDate": payload.get("dueDate", existing["due_date"]),
                    "note": payload.get("note", existing["note"]),
                    "archived": payload.get("archived", bool(existing["archived"])),
                    "createdAt": existing["created_at"],
                },
                allow_id=True,
            )
            con.execute(
                """
                UPDATE debts
                SET person = :person,
                    direction = :direction,
                    amount_cents = :amount_cents,
                    due_date = :due_date,
                    note = :note,
                    archived = :archived
                WHERE id = :id
                """,
                debt,
            )
            row = con.execute(
                """
                SELECT d.*, COALESCE(SUM(p.amount_cents), 0) AS paid_cents
                FROM debts d
                LEFT JOIN debt_payments p ON p.debt_id = d.id
                WHERE d.id = ?
                GROUP BY d.id
                """,
                (debt_id,),
            ).fetchone()
        self.send_json(200, {"debt": debt_to_json(row)})

    def delete_debt(self, debt_id):
        debt_id = clean_id(debt_id)
        with connect() as con:
            cursor = con.execute("DELETE FROM debts WHERE id = ?", (debt_id,))
            if cursor.rowcount == 0:
                raise ApiError(404, "Долг не найден")
        self.send_json(200, {"ok": True})

    def create_debt_payment(self):
        payload = self.read_json()
        with connect() as con:
            debts = con.execute("SELECT id FROM debts").fetchall()
            debt_ids = {row["id"] for row in debts}
            payment = normalize_debt_payment(payload, debt_ids=debt_ids)
            con.execute(
                """
                INSERT INTO debt_payments
                    (id, debt_id, amount_cents, date, note, created_at)
                VALUES
                    (:id, :debt_id, :amount_cents, :date, :note, :created_at)
                """,
                payment,
            )
            row = con.execute(
                "SELECT * FROM debt_payments WHERE id = ?", (payment["id"],)
            ).fetchone()
        self.send_json(201, {"payment": debt_payment_to_json(row)})

    def delete_debt_payment(self, payment_id):
        payment_id = clean_id(payment_id)
        with connect() as con:
            cursor = con.execute("DELETE FROM debt_payments WHERE id = ?", (payment_id,))
            if cursor.rowcount == 0:
                raise ApiError(404, "Платеж не найден")
        self.send_json(200, {"ok": True})

    def update_transaction(self, transaction_id):
        transaction_id = clean_id(transaction_id)
        payload = self.read_json()
        with connect() as con:
            existing = con.execute(
                "SELECT * FROM transactions WHERE id = ?", (transaction_id,)
            ).fetchone()
            if existing is None:
                raise ApiError(404, "Операция не найдена")
            categories = con.execute("SELECT id, type FROM categories").fetchall()
            category_types = {row["id"]: row["type"] for row in categories}
            accounts = con.execute("SELECT id FROM accounts").fetchall()
            account_ids = {row["id"] for row in accounts}
            transaction = normalize_transaction(
                {
                    "id": transaction_id,
                    "type": payload.get("type", existing["type"]),
                    "accountId": payload.get("accountId", existing["account_id"]),
                    "toAccountId": payload.get("toAccountId", existing["to_account_id"]),
                    "categoryId": payload.get("categoryId", existing["category_id"]),
                    "amountCents": payload.get("amountCents", existing["amount_cents"]),
                    "date": payload.get("date", existing["date"]),
                    "note": payload.get("note", existing["note"]),
                    "createdAt": existing["created_at"],
                },
                category_types,
                account_ids,
                allow_id=True,
            )
            if transaction["type"] == "expense" and transaction["account_id"] == "account-shared":
                raise ApiError(
                    409,
                    "Общий расход создается двумя половинами. Создайте новую общую трату.",
                )
            con.execute(
                """
                UPDATE transactions
                SET type = :type,
                    account_id = :account_id,
                    to_account_id = :to_account_id,
                    category_id = :category_id,
                    amount_cents = :amount_cents,
                    date = :date,
                    note = :note
                WHERE id = :id
                """,
                transaction,
            )
            row = con.execute(
                "SELECT * FROM transactions WHERE id = ?", (transaction_id,)
            ).fetchone()
        self.send_json(200, {"transaction": transaction_to_json(row)})

    def create_balance_adjustment(self):
        payload = self.read_json()
        account_id = clean_id(payload.get("accountId"), "accountId")
        month = clean_string(payload.get("month"), "month", 7)
        if not MONTH_RE.match(month):
            raise ApiError(400, "month: нужен формат YYYY-MM")
        target_cents = clean_balance_cents(payload.get("balanceCents"), "balanceCents")
        date = clean_date(payload.get("date"))
        note = clean_optional_string(payload.get("note"), "note", 160) or "Коррекция баланса"

        with connect() as con:
            account = con.execute("SELECT id FROM accounts WHERE id = ?", (account_id,)).fetchone()
            if account is None:
                raise ApiError(400, "accountId: счет не найден")
            current_cents = account_balance_through_date(con, account_id, date)
            diff_cents = target_cents - current_cents
            if diff_cents == 0:
                self.send_json(200, {"ok": True, "skipped": True, "balanceCents": current_cents})
                return
            tx_type = "income" if diff_cents > 0 else "expense"
            category = fallback_category(con, tx_type)
            if category is None:
                raise ApiError(409, "Нужна категория для корректировки баланса")
            transaction = {
                "id": make_id("tx"),
                "type": tx_type,
                "account_id": account_id,
                "to_account_id": None,
                "category_id": category["id"],
                "amount_cents": abs(diff_cents),
                "date": date,
                "note": note,
                "is_balance_adjustment": 1,
                "created_at": now_iso(),
            }
            con.execute(
                """
                INSERT INTO transactions
                    (id, type, account_id, to_account_id, category_id, amount_cents, date, note,
                     is_balance_adjustment, created_at)
                VALUES
                    (:id, :type, :account_id, :to_account_id, :category_id, :amount_cents,
                     :date, :note, :is_balance_adjustment, :created_at)
                """,
                transaction,
            )
            row = con.execute(
                "SELECT * FROM transactions WHERE id = ?", (transaction["id"],)
            ).fetchone()
        self.send_json(201, {"transaction": transaction_to_json(row), "diffCents": diff_cents})

    def delete_transaction(self, transaction_id):
        transaction_id = clean_id(transaction_id)
        with connect() as con:
            cursor = con.execute("DELETE FROM transactions WHERE id = ?", (transaction_id,))
            if cursor.rowcount == 0:
                raise ApiError(404, "Операция не найдена")
        self.send_json(200, {"ok": True})

    def export_data(self):
        with connect() as con:
            accounts = con.execute(
                "SELECT * FROM accounts ORDER BY sort_order, name COLLATE NOCASE"
            ).fetchall()
            categories = con.execute(
                "SELECT * FROM categories ORDER BY type DESC, name COLLATE NOCASE"
            ).fetchall()
            transactions = con.execute(
                "SELECT * FROM transactions ORDER BY date DESC, created_at DESC"
            ).fetchall()
            goals = con.execute(
                "SELECT * FROM goals ORDER BY archived, created_at DESC"
            ).fetchall()
            debts = con.execute(
                """
                SELECT d.*, COALESCE(SUM(p.amount_cents), 0) AS paid_cents
                FROM debts d
                LEFT JOIN debt_payments p ON p.debt_id = d.id
                GROUP BY d.id
                ORDER BY d.archived, d.created_at DESC
                """
            ).fetchall()
            debt_payments = con.execute(
                "SELECT * FROM debt_payments ORDER BY date DESC, created_at DESC"
            ).fetchall()
        filename = f"finance-backup-{datetime.now().strftime('%Y-%m-%d')}.json"
        self.send_json(
            200,
            {
                "version": 5,
                "exportedAt": now_iso(),
                "accounts": [account_to_json(row) for row in accounts],
                "categories": [category_to_json(row) for row in categories],
                "transactions": [transaction_to_json(row) for row in transactions],
                "goals": [goal_to_json(row) for row in goals],
                "debts": [debt_to_json(row) for row in debts],
                "debtPayments": [debt_payment_to_json(row) for row in debt_payments],
            },
            {"Content-Disposition": f'attachment; filename="{filename}"'},
        )

    def import_data(self):
        payload = self.read_json()
        raw_accounts = payload.get("accounts")
        raw_categories = payload.get("categories")
        raw_transactions = payload.get("transactions")
        raw_goals = payload.get("goals", [])
        raw_debts = payload.get("debts", [])
        raw_debt_payments = payload.get("debtPayments", [])
        if not isinstance(raw_categories, list) or not isinstance(raw_transactions, list):
            raise ApiError(400, "Файл должен содержать categories и transactions")
        if not isinstance(raw_goals, list):
            raise ApiError(400, "goals: неверный формат")
        if not isinstance(raw_debts, list) or not isinstance(raw_debt_payments, list):
            raise ApiError(400, "debts: неверный формат")

        accounts = []
        account_ids = set()
        if raw_accounts is None:
            raw_accounts = [
                {
                    "id": account[0],
                    "name": account[1],
                    "color": account[2],
                    "sortOrder": account[3],
                    "system": bool(account[4]),
                    "kind": account[5],
                    "bank": account[6],
                    "owner": account[7],
                    "createdAt": now_iso(),
                }
                for account in DEFAULT_ACCOUNTS
            ]
        if not isinstance(raw_accounts, list):
            raise ApiError(400, "accounts: неверный формат")
        for raw_account in raw_accounts:
            if not isinstance(raw_account, dict):
                raise ApiError(400, "accounts: неверный формат")
            account = normalize_account(raw_account, allow_id=True)
            if account["id"] in account_ids:
                raise ApiError(400, "accounts: повторяется id")
            account_ids.add(account["id"])
            accounts.append(account)
        if not accounts:
            raise ApiError(400, "Нужен хотя бы один счет")

        categories = []
        category_ids = set()
        category_types = {}
        for raw_category in raw_categories:
            if not isinstance(raw_category, dict):
                raise ApiError(400, "categories: неверный формат")
            category = normalize_category(raw_category, allow_id=True)
            if category["id"] in category_ids:
                raise ApiError(400, "categories: повторяется id")
            category_ids.add(category["id"])
            category_types[category["id"]] = category["type"]
            categories.append(category)

        if not any(category["type"] == "expense" for category in categories):
            raise ApiError(400, "Нужна хотя бы одна категория расходов")
        if not any(category["type"] == "income" for category in categories):
            raise ApiError(400, "Нужна хотя бы одна категория доходов")

        transactions = []
        transaction_ids = set()
        for raw_transaction in raw_transactions:
            if not isinstance(raw_transaction, dict):
                raise ApiError(400, "transactions: неверный формат")
            transaction = normalize_transaction(
                raw_transaction,
                category_types=category_types,
                account_ids=account_ids,
                allow_id=True,
            )
            if transaction["id"] in transaction_ids:
                raise ApiError(400, "transactions: повторяется id")
            transaction_ids.add(transaction["id"])
            transactions.append(transaction)

        goals = []
        goal_ids = set()
        for raw_goal in raw_goals:
            if not isinstance(raw_goal, dict):
                raise ApiError(400, "goals: неверный формат")
            goal = normalize_goal(raw_goal, allow_id=True)
            if goal["id"] in goal_ids:
                raise ApiError(400, "goals: повторяется id")
            goal_ids.add(goal["id"])
            goals.append(goal)

        debts = []
        debt_ids = set()
        for raw_debt in raw_debts:
            if not isinstance(raw_debt, dict):
                raise ApiError(400, "debts: неверный формат")
            debt = normalize_debt(raw_debt, allow_id=True)
            if debt["id"] in debt_ids:
                raise ApiError(400, "debts: повторяется id")
            debt_ids.add(debt["id"])
            debts.append(debt)

        debt_payments = []
        payment_ids = set()
        for raw_payment in raw_debt_payments:
            if not isinstance(raw_payment, dict):
                raise ApiError(400, "debtPayments: неверный формат")
            payment = normalize_debt_payment(raw_payment, debt_ids=debt_ids, allow_id=True)
            if payment["id"] in payment_ids:
                raise ApiError(400, "debtPayments: повторяется id")
            payment_ids.add(payment["id"])
            debt_payments.append(payment)

        with connect() as con:
            con.execute("DELETE FROM debt_payments")
            con.execute("DELETE FROM debts")
            con.execute("DELETE FROM goals")
            con.execute("DELETE FROM transactions")
            con.execute("DELETE FROM accounts")
            con.execute("DELETE FROM categories")
            con.executemany(
                """
                INSERT INTO accounts
                    (id, name, color, sort_order, system, kind, bank, owner, created_at)
                VALUES
                    (:id, :name, :color, :sort_order, :system, :kind, :bank, :owner,
                     :created_at)
                """,
                accounts,
            )
            con.executemany(
                """
                INSERT INTO categories
                    (id, type, name, color, budget_cents, system, created_at)
                VALUES (:id, :type, :name, :color, :budget_cents, :system, :created_at)
                """,
                categories,
            )
            con.executemany(
                """
                INSERT INTO transactions
                    (id, type, account_id, to_account_id, category_id, amount_cents, date, note,
                     is_balance_adjustment, created_at)
                VALUES
                    (:id, :type, :account_id, :to_account_id, :category_id, :amount_cents,
                     :date, :note, :is_balance_adjustment, :created_at)
                """,
                transactions,
            )
            con.executemany(
                """
                INSERT INTO goals
                    (id, name, target_cents, saved_cents, target_date, color, archived, created_at)
                VALUES
                    (:id, :name, :target_cents, :saved_cents, :target_date, :color, :archived, :created_at)
                """,
                goals,
            )
            con.executemany(
                """
                INSERT INTO debts
                    (id, person, direction, amount_cents, due_date, note, archived, created_at)
                VALUES
                    (:id, :person, :direction, :amount_cents, :due_date, :note, :archived, :created_at)
                """,
                debts,
            )
            con.executemany(
                """
                INSERT INTO debt_payments
                    (id, debt_id, amount_cents, date, note, created_at)
                VALUES
                    (:id, :debt_id, :amount_cents, :date, :note, :created_at)
                """,
                debt_payments,
            )
        self.send_json(200, {"ok": True})


def main():
    init_db()
    server = ThreadingHTTPServer((HOST, PORT), FinanceHandler)
    print(f"Finance app is running on http://{HOST}:{PORT}")
    print(f"SQLite database: {DB_PATH}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nStopping finance app")
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
