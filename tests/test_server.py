import json
import tempfile
import threading
import unittest
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import server


class FinanceServerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.original_db_path = server.DB_PATH
        cls.temp_dir = tempfile.TemporaryDirectory(prefix="finance-tests-")
        server.DB_PATH = Path(cls.temp_dir.name) / "test-finance.db"
        cls.httpd = server.ThreadingHTTPServer(
            ("127.0.0.1", 0), server.FinanceHandler
        )
        cls.base_url = f"http://127.0.0.1:{cls.httpd.server_port}"
        cls.server_thread = threading.Thread(
            target=cls.httpd.serve_forever, daemon=True
        )
        cls.server_thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.httpd.shutdown()
        cls.httpd.server_close()
        cls.server_thread.join(timeout=5)
        server.DB_PATH = cls.original_db_path
        cls.temp_dir.cleanup()

    def setUp(self):
        for suffix in ("", "-journal", "-shm", "-wal"):
            candidate = Path(f"{server.DB_PATH}{suffix}")
            if candidate.exists():
                candidate.unlink()
        server.init_db()

    def request(self, path, method="GET", payload=None):
        data = None
        headers = {}
        if payload is not None:
            data = json.dumps(payload).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = Request(
            f"{self.base_url}{path}", data=data, headers=headers, method=method
        )
        try:
            with urlopen(request, timeout=5) as response:
                return response.status, response.headers, response.read()
        except HTTPError as error:
            response = (error.code, error.headers, error.read())
            error.close()
            return response

    def backup_fixture(self):
        created_at = "2026-01-02T03:04:05+00:00"
        return {
            "version": 5,
            "exportedAt": created_at,
            "accounts": [
                {
                    "id": "account-a",
                    "name": "Основной",
                    "color": "#112233",
                    "sortOrder": 1,
                    "system": False,
                    "kind": "regular",
                    "bank": "Тестовый банк",
                    "owner": "А",
                    "createdAt": created_at,
                },
                {
                    "id": "account-b",
                    "name": "Накопления",
                    "color": "#445566",
                    "sortOrder": 2,
                    "system": False,
                    "kind": "savings",
                    "bank": "Тестовый банк",
                    "owner": "Б",
                    "createdAt": created_at,
                },
            ],
            "categories": [
                {
                    "id": "income-test",
                    "type": "income",
                    "name": "Доход",
                    "color": "#227744",
                    "budgetCents": 0,
                    "system": False,
                    "createdAt": created_at,
                },
                {
                    "id": "expense-test",
                    "type": "expense",
                    "name": "Расход",
                    "color": "#992244",
                    "budgetCents": 5000,
                    "system": False,
                    "createdAt": created_at,
                },
            ],
            "transactions": [
                {
                    "id": "tx-income",
                    "type": "income",
                    "accountId": "account-a",
                    "toAccountId": None,
                    "categoryId": "income-test",
                    "amountCents": 10000,
                    "date": "2026-01-10",
                    "note": "Доход",
                    "isBalanceAdjustment": False,
                    "createdAt": created_at,
                },
                {
                    "id": "tx-expense",
                    "type": "expense",
                    "accountId": "account-a",
                    "toAccountId": None,
                    "categoryId": "expense-test",
                    "amountCents": 2500,
                    "date": "2026-01-11",
                    "note": "Расход",
                    "isBalanceAdjustment": False,
                    "createdAt": created_at,
                },
                {
                    "id": "tx-transfer",
                    "type": "transfer",
                    "accountId": "account-a",
                    "toAccountId": "account-b",
                    "categoryId": None,
                    "amountCents": 3000,
                    "date": "2026-01-12",
                    "note": "Перевод",
                    "isBalanceAdjustment": False,
                    "createdAt": created_at,
                },
            ],
            "goals": [
                {
                    "id": "goal-test",
                    "name": "Резерв",
                    "targetCents": 50000,
                    "savedCents": 12000,
                    "targetDate": "2026-12-31",
                    "color": "#556677",
                    "archived": False,
                    "createdAt": created_at,
                }
            ],
            "debts": [
                {
                    "id": "debt-test",
                    "person": "Тест",
                    "direction": "owe_me",
                    "amountCents": 15000,
                    "paidCents": 4000,
                    "leftCents": 11000,
                    "dueDate": "2026-06-30",
                    "note": "Долг",
                    "archived": False,
                    "createdAt": created_at,
                }
            ],
            "debtPayments": [
                {
                    "id": "pay-test",
                    "debtId": "debt-test",
                    "amountCents": 4000,
                    "date": "2026-02-01",
                    "note": "Часть долга",
                    "createdAt": created_at,
                }
            ],
        }

    def test_only_required_frontend_files_are_public(self):
        expected_types = {
            "/": "text/html",
            "/styles.css": "text/css",
            "/app.js": "text/javascript",
        }
        for path, content_type in expected_types.items():
            with self.subTest(path=path):
                status, headers, body = self.request(path)
                self.assertEqual(status, 200)
                self.assertTrue(body)
                self.assertIn(content_type, headers["Content-Type"])

        blocked_paths = (
            "/finance.db",
            "/server.py",
            "/%73erver.py",
            "/.git/config",
            "/README.md",
            "/finance-backup-2026-01-01.json",
            "/unknown-file.txt",
        )
        for path in blocked_paths:
            with self.subTest(path=path):
                status, _, _ = self.request(path)
                self.assertEqual(status, 404)

    def test_health_does_not_disclose_internal_details(self):
        status, _, body = self.request("/api/health")
        payload = json.loads(body)
        self.assertEqual(status, 200)
        self.assertEqual(payload, {"ok": True})
        self.assertNotIn(str(server.DB_PATH), body.decode("utf-8"))

    def test_security_headers_are_present_on_success_and_error(self):
        for path in ("/", "/api/health", "/missing"):
            with self.subTest(path=path):
                _, headers, _ = self.request(path)
                self.assertEqual(headers["X-Content-Type-Options"], "nosniff")
                self.assertEqual(headers["X-Frame-Options"], "DENY")
                self.assertEqual(headers["Referrer-Policy"], "no-referrer")
                self.assertEqual(headers["Cross-Origin-Opener-Policy"], "same-origin")
                self.assertIn("default-src 'self'", headers["Content-Security-Policy"])
                self.assertIn("frame-ancestors 'none'", headers["Content-Security-Policy"])

    def test_version_5_backup_import_export_round_trip(self):
        fixture = self.backup_fixture()
        status, _, body = self.request("/api/import", "POST", fixture)
        self.assertEqual((status, json.loads(body)), (200, {"ok": True}))

        status, headers, body = self.request("/api/export")
        exported = json.loads(body)
        self.assertEqual(status, 200)
        self.assertEqual(exported["version"], 5)
        self.assertIn("attachment;", headers["Content-Disposition"])
        self.assertEqual(exported["accounts"], fixture["accounts"])
        self.assertEqual(exported["categories"], fixture["categories"])
        self.assertEqual(
            sorted(exported["transactions"], key=lambda item: item["id"]),
            sorted(fixture["transactions"], key=lambda item: item["id"]),
        )
        self.assertEqual(exported["goals"], fixture["goals"])
        self.assertEqual(exported["debtPayments"], fixture["debtPayments"])
        self.assertEqual(exported["debts"], fixture["debts"])

        status, _, _ = self.request("/api/import", "POST", exported)
        self.assertEqual(status, 200)
        status, _, body = self.request("/api/export")
        round_tripped = json.loads(body)
        self.assertEqual(status, 200)
        for field in (
            "accounts",
            "categories",
            "transactions",
            "goals",
            "debts",
            "debtPayments",
        ):
            self.assertEqual(round_tripped[field], exported[field])

    def test_financial_calculation_regressions(self):
        status, _, _ = self.request("/api/import", "POST", self.backup_fixture())
        self.assertEqual(status, 200)
        with server.connect() as con:
            self.assertEqual(
                server.account_balance_through_date(con, "account-a", "2026-01-31"),
                4500,
            )
            self.assertEqual(
                server.account_balance_through_date(con, "account-b", "2026-01-31"),
                3000,
            )

        split = server.split_shared_expense(
            {
                "id": "source",
                "type": "expense",
                "account_id": "account-shared",
                "to_account_id": None,
                "category_id": "expense-test",
                "amount_cents": 101,
                "date": "2026-01-01",
                "note": "Общий чек",
                "is_balance_adjustment": 0,
                "created_at": "2026-01-01T00:00:00+00:00",
            }
        )
        self.assertEqual([item["amount_cents"] for item in split], [50, 51])
        self.assertEqual(sum(item["amount_cents"] for item in split), 101)


if __name__ == "__main__":
    unittest.main()
