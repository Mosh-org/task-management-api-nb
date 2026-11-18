import glob, os, uuid
from datetime import datetime, timedelta, timezone
import pytest, requests

BASE = "http://localhost:8080"
API = BASE + "/api/v1/tasks"
T = 10


def iso(delta_days):
    return (datetime.now(timezone.utc) + timedelta(days=delta_days)).strftime("%Y-%m-%dT%H:%M:%SZ")


def mk(title, due=None, **kw):
    body = {"title": title, **kw}
    if due is not None:
        body["due_date"] = due
    r = requests.post(API, json=body, timeout=T)
    assert r.status_code == 201, r.text
    return r.json()


def setstatus(tid, st):
    r = requests.patch(f"{API}/{tid}/status", json={"status": st}, timeout=T)
    assert r.status_code == 200, r.text
    return r.json()


def overdue_all(size=100):
    out, tok = [], ""
    while True:
        p = {"page_size": size}
        if tok:
            p["page_token"] = tok
        r = requests.get(API + "/overdue", params=p, timeout=T)
        assert r.status_code == 200, r.text
        j = r.json()
        out += j["tasks"]
        tok = j.get("next_page_token", "")
        if not tok:
            return out, j["total_count"]


@pytest.fixture(autouse=True)
def health():
    assert requests.get(BASE + "/health", timeout=T).status_code == 200


@pytest.fixture
def created():
    ids = []
    yield ids
    for i in ids:
        requests.delete(f"{API}/{i}", timeout=T)


def track(created, t):
    created.append(t["id"])
    return t


class TestOverdue:
    def test_overdue_filter_and_sort(self, created):
        tag = "ftrun_" + uuid.uuid4().hex[:6]
        old = track(created, mk(tag + "_old", iso(-30)))
        mid = track(created, mk(tag + "_mid", iso(-5)))
        fut = track(created, mk(tag + "_fut", iso(5)))
        nodue = track(created, mk(tag + "_nodue"))
        done = track(created, mk(tag + "_done", iso(-10)))
        canc = track(created, mk(tag + "_canc", iso(-10)))
        hold = track(created, mk(tag + "_hold", iso(-2)))
        setstatus(done["id"], "completed")
        setstatus(canc["id"], "cancelled")
        setstatus(hold["id"], "on_hold")
        tasks, total = overdue_all()
        ids = [t["id"] for t in tasks]
        assert total == len(tasks)
        assert old["id"] in ids and mid["id"] in ids and hold["id"] in ids
        for x in (fut, nodue, done, canc):
            assert x["id"] not in ids
        dues = [t["due_date"] for t in tasks]
        assert dues == sorted(dues)
        assert ids.index(old["id"]) < ids.index(mid["id"]) < ids.index(hold["id"])

    def test_overdue_not_captured_by_id(self):
        r = requests.get(API + "/overdue", timeout=T)
        assert r.status_code == 200
        j = r.json()
        assert set(["tasks", "total_count"]) <= set(j)
        assert isinstance(j["tasks"], list)

    def test_tiebreak_created_at_then_id(self, created):
        due = iso(-20)
        a = track(created, mk("ftrun_tie_a", due))
        b = track(created, mk("ftrun_tie_b", due))
        tasks, _ = overdue_all()
        ids = [t["id"] for t in tasks]
        assert ids.index(a["id"]) < ids.index(b["id"])

    def test_pagination(self, created):
        for i in range(3):
            track(created, mk(f"ftrun_pg_{i}", iso(-40 - i)))
        r = requests.get(API + "/overdue", params={"page_size": 2}, timeout=T)
        assert r.status_code == 200
        j = r.json()
        assert len(j["tasks"]) == 2 and j["next_page_token"]
        assert j["total_count"] >= 3
        r2 = requests.get(API + "/overdue", params={"page_size": 2, "page_token": j["next_page_token"]}, timeout=T).json()
        assert r2["tasks"] and r2["tasks"][0]["id"] not in [t["id"] for t in j["tasks"]]
        assert j["next_page_token"] == "2"

    def test_last_page_no_token(self):
        j = requests.get(API + "/overdue", params={"page_size": 100}, timeout=T).json()
        if j["total_count"] <= 100:
            assert not j.get("next_page_token")

    def test_default_page_size_20(self, created):
        for i in range(22):
            track(created, mk(f"ftrun_ps_{i}", iso(-60)))
        j = requests.get(API + "/overdue", timeout=T).json()
        assert len(j["tasks"]) == 20 and j["next_page_token"]

    @pytest.mark.parametrize("v", ["0", "101", "-1", "abc"])
    def test_invalid_page_size(self, v):
        r = requests.get(API + "/overdue", params={"page_size": v}, timeout=T)
        assert r.status_code == 400
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_page_size_max_ok(self):
        assert requests.get(API + "/overdue", params={"page_size": 100}, timeout=T).status_code == 200

    def test_invalid_page_token(self):
        # spec only mandates 400 for page_size; token handling must match the list endpoint
        r = requests.get(API + "/overdue", params={"page_token": "abc"}, timeout=T)
        l = requests.get(API, params={"page_token": "abc"}, timeout=T)
        assert r.status_code == l.status_code

    def test_status_change_moves_task_out(self, created):
        t = track(created, mk("ftrun_move", iso(-3)))
        assert t["id"] in [x["id"] for x in overdue_all()[0]]
        setstatus(t["id"], "completed")
        assert t["id"] not in [x["id"] for x in overdue_all()[0]]


class TestAssign:
    def test_assign_ok(self, created):
        t = track(created, mk("ftrun_assign"))
        r = requests.post(f"{API}/{t['id']}/assign", json={"email": "alice@example.com"}, timeout=T)
        assert r.status_code == 200, r.text
        j = r.json()
        assert j["id"] == t["id"] and j["assigned_to"] == "alice@example.com"
        g = requests.get(f"{API}/{t['id']}", timeout=T).json()
        assert g["assigned_to"] == "alice@example.com"

    def test_assign_logs_notification(self, created):
        logs = glob.glob(os.path.expandvars("$MCODE_DIR/lifecycle/target/runs/*_run_std*.log"))
        if not logs:
            pytest.skip("no server log available")
        t = track(created, mk("ftrun_notify"))
        r = requests.post(f"{API}/{t['id']}/assign", json={"email": "n@example.com"}, timeout=T)
        assert r.status_code == 200
        content = "".join(open(p, errors="ignore").read() for p in logs)
        assert f"notification: task {t['id']}" in content and "n@example.com" in content

    def test_reassign(self, created):
        t = track(created, mk("ftrun_reassign"))
        requests.post(f"{API}/{t['id']}/assign", json={"email": "a@example.com"}, timeout=T)
        r = requests.post(f"{API}/{t['id']}/assign", json={"email": "b@example.com"}, timeout=T)
        assert r.status_code == 200 and r.json()["assigned_to"] == "b@example.com"

    @pytest.mark.parametrize("body", [{"email": "not-an-email"}, {"email": ""}, {}, {"email": 5}, {"email": "a@"}])
    def test_assign_invalid_email(self, created, body):
        t = track(created, mk("ftrun_badmail"))
        r = requests.post(f"{API}/{t['id']}/assign", json=body, timeout=T)
        assert r.status_code == 400, r.text
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"
        assert requests.get(f"{API}/{t['id']}", timeout=T).json().get("assigned_to") is None

    def test_assign_no_body(self, created):
        t = track(created, mk("ftrun_nobody"))
        r = requests.post(f"{API}/{t['id']}/assign", timeout=T)
        assert r.status_code == 400

    def test_assign_unknown_task(self):
        r = requests.post(f"{API}/does-not-exist/assign", json={"email": "a@example.com"}, timeout=T)
        assert r.status_code == 404
        assert r.json()["error"]["code"] == "NOT_FOUND"


class TestRegression:
    def test_health(self):
        r = requests.get(BASE + "/health", timeout=T)
        assert r.status_code == 200

    def test_list_and_pagination(self):
        # Walk the whole default-ordered list with page_size=1: must be stable (no dups/omissions)
        for _ in range(8):
            seen, tok, total = [], "", None
            while True:
                p = {"page_size": 1}
                if tok:
                    p["page_token"] = tok
                r = requests.get(API, params=p, timeout=T)
                assert r.status_code == 200
                j = r.json()
                total = j["total_count"]
                seen += [t["id"] for t in j["tasks"]]
                tok = j.get("next_page_token", "")
                if not tok:
                    break
            assert len(seen) == len(set(seen)) == total, "unstable default ordering across pages"

    def test_search(self, created):
        tag = "ftrunsearch" + uuid.uuid4().hex[:6]
        t = track(created, mk(tag))
        j = requests.get(API + "/search", params={"q": tag}, timeout=T).json()
        assert [x["id"] for x in j["tasks"]] == [t["id"]]

    def test_stats(self):
        r = requests.get(API + "/stats", timeout=T)
        assert r.status_code == 200
        j = r.json()
        assert j["total"] == sum(j["by_status"].values()) == sum(j["by_priority"].values())

    def test_create_get_put_patch_delete(self, created):
        t = mk("ftrun_crud", priority="high", tags=["a"])
        created.append(t["id"])
        assert t["status"] == "pending" and t["priority"] == "high"
        assert requests.get(f"{API}/{t['id']}", timeout=T).json()["title"] == "ftrun_crud"
        r = requests.put(f"{API}/{t['id']}", json={"title": "ftrun_crud2"}, timeout=T)
        assert r.status_code == 200 and r.json()["title"] == "ftrun_crud2"
        assert setstatus(t["id"], "completed")["status"] == "completed"
        assert requests.delete(f"{API}/{t['id']}", timeout=T).status_code in (200, 204)
        assert requests.get(f"{API}/{t['id']}", timeout=T).status_code == 404

    def test_get_not_found(self):
        r = requests.get(API + "/nope", timeout=T)
        assert r.status_code == 404 and r.json()["error"]["code"] == "NOT_FOUND"

    def test_create_missing_title(self):
        r = requests.post(API, json={"description": "x"}, timeout=T)
        assert r.status_code == 400 and r.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_patch_invalid_status(self, created):
        t = track(created, mk("ftrun_badstatus"))
        r = requests.patch(f"{API}/{t['id']}/status", json={"status": "bogus"}, timeout=T)
        assert r.status_code == 400

    def test_comments(self, created):
        t = track(created, mk("ftrun_comments"))
        r = requests.post(f"{API}/{t['id']}/comments", json={"body": "hello", "author": "bob"}, timeout=T)
        assert r.status_code == 201, r.text
        cid = r.json()["id"]
        lst = requests.get(f"{API}/{t['id']}/comments", timeout=T)
        assert lst.status_code == 200 and cid in lst.text
        assert requests.delete(f"{API}/{t['id']}/comments/{cid}", timeout=T).status_code in (200, 204)
