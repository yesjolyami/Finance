import json
import mimetypes
import os
import re
import sqlite3
import uuid
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse


ROOT = Path(__file__).resolve().parent
DB_PATH = Path(os.environ.get("FINANCE_DB", ROOT / "finance.db")).resolve()
HOST = os.environ.get("HOST", "127.0.0.1")
PORT = int(os.environ.get("PORT", "3212"))

DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
MONTH_RE = re.compile(r"^\d{4}-\d{2}$")
HEX_RE = re.compile(r"^#[0-9a-fA-F]{6}$")
ID_RE = re.compile(r"^[a-zA-Z0-9_.:-]{1,96}$")
VALID_TYPES = {"expense", "income"}

DEFAULT_ACCOUNTS = [
    ("account-shared", "Общий", "#fb96ff", 1, 1),
    ("account-rina", "Котик Рины", "#173921", 2, 1),
    ("account-valera", "Котик Валеры", "#4f6078", 3, 1),
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


def connect():
    connection = sqlite3.connect(DB_PATH)
    connection.row_factory = sqlite3.Row
    connection.execute("PRAGMA foreign_keys = ON")
    return connection


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
                type TEXT NOT NULL CHECK (type IN ('expense', 'income')),
                account_id TEXT NOT NULL DEFAULT 'account-shared',
                category_id TEXT NOT NULL,
                amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
                date TEXT NOT NULL,
                note TEXT NOT NULL DEFAULT '',
                created_at TEXT NOT NULL,
                FOREIGN KEY (account_id) REFERENCES accounts(id)
                    ON DELETE RESTRICT,
                FOREIGN KEY (category_id) REFERENCES categories(id)
                    ON DELETE RESTRICT
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
            """
        )

        account_count = con.execute("SELECT COUNT(*) AS total FROM accounts").fetchone()[
            "total"
        ]
        if account_count == 0:
            created_at = now_iso()
            con.executemany(
                """
                INSERT INTO accounts
                    (id, name, color, sort_order, system, created_at)
                VALUES (?, ?, ?, ?, ?, ?)
                """,
                [(*account, created_at) for account in DEFAULT_ACCOUNTS],
            )

        transaction_columns = {
            row["name"] for row in con.execute("PRAGMA table_info(transactions)").fetchall()
        }
        if "account_id" not in transaction_columns:
            con.execute(
                """
                ALTER TABLE transactions
                ADD COLUMN account_id TEXT NOT NULL DEFAULT 'account-shared'
                """
            )
        con.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_transactions_account
            ON transactions(account_id)
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


def account_to_json(row):
    return {
        "id": row["id"],
        "name": row["name"],
        "color": row["color"],
        "sortOrder": row["sort_order"],
        "system": bool(row["system"]),
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
        "categoryId": row["category_id"],
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
        raise ApiError(400, "type: допустимы только expense или income")
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
        "name": clean_string(payload.get("name"), "name", 36),
        "color": clean_color(payload.get("color", "#6b7280")),
        "sort_order": clean_cents(
            payload.get("sortOrder", 0), "sortOrder", allow_zero=True
        ),
        "system": 1 if bool(payload.get("system", False)) else 0,
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }


def normalize_transaction(
    payload, category_types=None, account_ids=None, allow_id=False
):
    transaction = {
        "id": clean_id(payload.get("id")) if allow_id else make_id("tx"),
        "type": clean_type(payload.get("type")),
        "account_id": clean_id(payload.get("accountId", "account-shared"), "accountId"),
        "category_id": clean_id(payload.get("categoryId"), "categoryId"),
        "amount_cents": clean_cents(payload.get("amountCents"), "amountCents"),
        "date": clean_date(payload.get("date")),
        "note": clean_optional_string(payload.get("note"), "note", 160),
        "created_at": clean_optional_string(payload.get("createdAt"), "createdAt", 40)
        or now_iso(),
    }
    if category_types is not None:
        category_type = category_types.get(transaction["category_id"])
        if category_type is None:
            raise ApiError(400, "categoryId: категория не найдена")
        if category_type != transaction["type"]:
            raise ApiError(400, "categoryId: тип категории не совпадает с операцией")
    if account_ids is not None and transaction["account_id"] not in account_ids:
        raise ApiError(400, "accountId: счет не найден")
    return transaction


class FinanceHandler(BaseHTTPRequestHandler):
    server_version = "PersonalFinance/1.0"

    def do_GET(self):
        self.route("GET")

    def do_POST(self):
        self.route("POST")

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
            self.send_json(200, {"ok": True, "database": str(DB_PATH)})
            return
        if method == "GET" and path == "/api/categories":
            self.get_categories()
            return
        if method == "GET" and path == "/api/accounts":
            self.get_accounts()
            return
        if method == "POST" and path == "/api/categories":
            self.create_category()
            return
        if method == "GET" and path == "/api/transactions":
            self.get_transactions(query)
            return
        if method == "POST" and path == "/api/transactions":
            self.create_transaction()
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
        self.send_header("X-Content-Type-Options", "nosniff")
        if extra_headers:
            for key, value in extra_headers.items():
                self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def serve_static(self, path):
        cleaned = unquote(path).split("?", 1)[0]
        requested = "index.html" if cleaned in {"", "/"} else cleaned.lstrip("/")
        target = (ROOT / requested).resolve()
        if target != ROOT and ROOT not in target.parents:
            raise ApiError(404, "Файл не найден")
        if target.is_dir():
            target = target / "index.html"
        if not target.exists() or not target.is_file():
            raise ApiError(404, "Файл не найден")

        content = target.read_bytes()
        content_type = mimetypes.guess_type(str(target))[0] or "application/octet-stream"
        if target.suffix in {".html", ".css", ".js"}:
            content_type += "; charset=utf-8"
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(content)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
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
            clauses.append("account_id = ?")
            params.append(clean_id(account_id, "account"))

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
            transaction = normalize_transaction(payload, category_types, account_ids)
            con.execute(
                """
                INSERT INTO transactions
                    (id, type, account_id, category_id, amount_cents, date, note, created_at)
                VALUES (:id, :type, :account_id, :category_id, :amount_cents, :date, :note, :created_at)
                """,
                transaction,
            )
            row = con.execute(
                "SELECT * FROM transactions WHERE id = ?", (transaction["id"],)
            ).fetchone()
        self.send_json(201, {"transaction": transaction_to_json(row)})

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
        filename = f"finance-backup-{datetime.now().strftime('%Y-%m-%d')}.json"
        self.send_json(
            200,
            {
                "version": 2,
                "exportedAt": now_iso(),
                "accounts": [account_to_json(row) for row in accounts],
                "categories": [category_to_json(row) for row in categories],
                "transactions": [transaction_to_json(row) for row in transactions],
            },
            {"Content-Disposition": f'attachment; filename="{filename}"'},
        )

    def import_data(self):
        payload = self.read_json()
        raw_accounts = payload.get("accounts")
        raw_categories = payload.get("categories")
        raw_transactions = payload.get("transactions")
        if not isinstance(raw_categories, list) or not isinstance(raw_transactions, list):
            raise ApiError(400, "Файл должен содержать categories и transactions")

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

        with connect() as con:
            con.execute("DELETE FROM transactions")
            con.execute("DELETE FROM accounts")
            con.execute("DELETE FROM categories")
            con.executemany(
                """
                INSERT INTO accounts
                    (id, name, color, sort_order, system, created_at)
                VALUES (:id, :name, :color, :sort_order, :system, :created_at)
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
                    (id, type, account_id, category_id, amount_cents, date, note, created_at)
                VALUES (:id, :type, :account_id, :category_id, :amount_cents, :date, :note, :created_at)
                """,
                transactions,
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
