import uuid
from datetime import datetime, timedelta, timezone
import pytest
import requests

BASE = "http://localhost:8080"
API = BASE + "/api/v1/tasks"
RUN = "ftrun_" + uuid.uuid4().hex[:6]


def iso(days):
    return (datetime.now(timezone.utc) + timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")


def mk(title, due=None, **kw):
    body = {"title": f"{RUN}_{title}"}
    if due is not None:
        body["due_date"] = iso(due)
    body.update(kw)
    r = requests.post(API, json=body, timeout=10)
    assert r.status_code == 201, r.text
    return r.json()


def overdue_all():
    out, tok = [], ""
    while True:
        r = requests.get(API + "/overdue", params={"page_size": 100, "page_token": tok}, timeout=10)
        assert r.status_code == 200
        d = r.json()
        out += d["tasks"]
        tok = d.get("next_page_token", "")
        if not tok:
            return out


@pytest.fixture(scope="module")
def seeded():
    t = {
        "old": mk("old", -10),
        "recent": mk("recent", -2),
        "future": mk("future", 5),
        "nodue": mk("nodue"),
        "done": mk("done", -5),
        "cancelled": mk("cancelled", -4),
    }
    assert requests.patch(f"{API}/{t['done']['id']}/status", json={"status": "completed"}, timeout=10).status_code == 200
    assert requests.patch(f"{API}/{t['cancelled']['id']}/status", json={"status": "cancelled"}, timeout=10).status_code == 200
    yield t
    for x in t.values():
        requests.delete(f"{API}/{x['id']}", timeout=10)


def test_health():
    assert requests.get(BASE + "/health", timeout=10).status_code == 200


def test_overdue_happy_filter_and_order(seeded):
    ids = [x["id"] for x in overdue_all()]
    assert seeded["old"]["id"] in ids and seeded["recent"]["id"] in ids
    for k in ("future", "nodue", "done", "cancelled"):
        assert seeded[k]["id"] not in ids
    assert ids.index(seeded["old"]["id"]) < ids.index(seeded["recent"]["id"])
    dues = [x["due_date"] for x in overdue_all()]
    assert dues == sorted(dues)


def test_overdue_precedence_over_id():
    r = requests.get(API + "/overdue", timeout=10)
    assert r.status_code == 200 and "tasks" in r.json()


def test_overdue_pagination(seeded):
    r = requests.get(API + "/overdue", params={"page_size": 1}, timeout=10)
    assert r.status_code == 200
    d = r.json()
    assert len(d["tasks"]) == 1 and d.get("next_page_token")
    r2 = requests.get(API + "/overdue", params={"page_size": 1, "page_token": d["next_page_token"]}, timeout=10)
    assert r2.status_code == 200
    assert r2.json()["tasks"][0]["id"] != d["tasks"][0]["id"]


@pytest.mark.parametrize("ps", ["0", "101", "-1", "abc"])
def test_overdue_invalid_page_size(ps):
    assert requests.get(API + "/overdue", params={"page_size": ps}, timeout=10).status_code == 400


def test_overdue_page_size_max_ok():
    assert requests.get(API + "/overdue", params={"page_size": 100}, timeout=10).status_code == 200


def test_assign_happy():
    t = mk("assign")
    r = requests.post(f"{API}/{t['id']}/assign", json={"email": "alice@example.com"}, timeout=10)
    assert r.status_code == 200
    assert r.json()["id"] == t["id"] and r.json()["assigned_to"] == "alice@example.com"
    g = requests.get(f"{API}/{t['id']}", timeout=10).json()
    assert g["assigned_to"] == "alice@example.com"
    requests.delete(f"{API}/{t['id']}", timeout=10)


@pytest.mark.parametrize("body", [{"email": "not-an-email"}, {}, {"email": ""}, {"email": 5}])
def test_assign_invalid_email(body):
    t = mk("assign_bad")
    r = requests.post(f"{API}/{t['id']}/assign", json=body, timeout=10)
    assert r.status_code == 400
    requests.delete(f"{API}/{t['id']}", timeout=10)


def test_assign_unknown_task():
    r = requests.post(f"{API}/does-not-exist/assign", json={"email": "a@b.com"}, timeout=10)
    assert r.status_code == 404


def test_regression_crud_comments_search_stats():
    t = mk("crud", 3)
    tid = t["id"]
    assert requests.get(f"{API}/{tid}", timeout=10).status_code == 200
    assert requests.put(f"{API}/{tid}", json={"description": "x"}, timeout=10).status_code == 200
    assert requests.patch(f"{API}/{tid}/status", json={"status": "in_progress"}, timeout=10).status_code == 200
    c = requests.post(f"{API}/{tid}/comments", json={"author": "a", "body": "b"}, timeout=10)
    assert c.status_code == 201
    assert requests.get(f"{API}/{tid}/comments", timeout=10).json()["total"] == 1
    assert requests.get(API, timeout=10).status_code == 200
    assert requests.get(API + "/search", params={"q": RUN}, timeout=10).status_code == 200
    assert requests.get(API + "/stats", timeout=10).status_code == 200
    assert requests.delete(f"{API}/{tid}", timeout=10).status_code in (200, 204)
    assert requests.get(f"{API}/{tid}", timeout=10).status_code == 404
