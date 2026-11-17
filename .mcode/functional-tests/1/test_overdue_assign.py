import uuid, datetime as dt, requests, pytest

BASE = "http://localhost:8080"
T = BASE + "/api/v1/tasks"
RUN = "ftrun_" + uuid.uuid4().hex[:6]
H = {"Content-Type": "application/json"}


def iso(days):
    return (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")


def mk(name, due=None, status=None):
    body = {"title": f"{RUN}_{name}"}
    if due:
        body["due_date"] = due
    r = requests.post(T, json=body, headers=H, timeout=10)
    assert r.status_code == 201, r.text
    t = r.json()
    if status:
        r = requests.patch(f"{T}/{t['id']}/status", json={"status": status}, headers=H, timeout=10)
        assert r.status_code == 200, r.text
    return t


def all_overdue():
    out, tok = [], None
    while True:
        p = {"page_size": 100}
        if tok:
            p["page_token"] = tok
        r = requests.get(T + "/overdue", params=p, timeout=10)
        assert r.status_code == 200
        j = r.json()
        out += j["tasks"]
        tok = j.get("next_page_token")
        if not tok:
            return out


@pytest.fixture(scope="module")
def data():
    d = {
        "old": mk("old", "2019-01-01T00:00:00Z"),
        "older": mk("older", "2018-01-01T00:00:00Z"),
        "tie1": mk("tie1", "2019-06-01T00:00:00Z"),
        "tie2": mk("tie2", "2019-06-01T00:00:00Z"),
        "future": mk("future", iso(30)),
        "nodue": mk("nodue"),
        "done": mk("done", "2019-01-01T00:00:00Z", "completed"),
        "canc": mk("canc", "2019-01-01T00:00:00Z", "cancelled"),
        "prog": mk("prog", "2019-01-02T00:00:00Z", "in_progress"),
    }
    return d


def test_health():
    assert requests.get(BASE + "/health", timeout=10).status_code == 200


def test_overdue_filter(data):
    ids = {t["id"] for t in all_overdue()}
    for k in ("old", "older", "tie1", "tie2", "prog"):
        assert data[k]["id"] in ids, k
    for k in ("future", "nodue", "done", "canc"):
        assert data[k]["id"] not in ids, k


def test_overdue_not_shadowed_and_shape(data):
    r = requests.get(T + "/overdue", timeout=10)
    assert r.status_code == 200
    j = r.json()
    assert "tasks" in j and "total_count" in j
    assert j["total_count"] >= 5


def test_overdue_order(data):
    mine = [t for t in all_overdue() if t["title"].startswith(RUN)]
    dues = [t["due_date"] for t in mine]
    assert dues == sorted(dues)
    tie = [t["id"] for t in mine if t["due_date"].startswith("2019-06-01")]
    assert tie == sorted(tie)
    assert mine[0]["id"] == data["older"]["id"]


def test_overdue_pagination(data):
    r = requests.get(T + "/overdue", params={"page_size": 1}, timeout=10)
    j = r.json()
    assert len(j["tasks"]) == 1 and j.get("next_page_token")
    total = j["total_count"]
    seen = [j["tasks"][0]["id"]]
    tok = j["next_page_token"]
    while tok:
        j = requests.get(T + "/overdue", params={"page_size": 1, "page_token": tok}, timeout=10).json()
        seen += [t["id"] for t in j["tasks"]]
        tok = j.get("next_page_token")
    assert len(seen) == len(set(seen)) == total


def test_overdue_page_size_max_and_default(data):
    r = requests.get(T + "/overdue", params={"page_size": 100}, timeout=10)
    assert r.status_code == 200 and len(r.json()["tasks"]) <= 100
    # matches ListTasks: values above max are rejected
    assert requests.get(T + "/overdue", params={"page_size": 101}, timeout=10).status_code == 400
    assert requests.get(T, params={"page_size": 101}, timeout=10).status_code == 400
    r = requests.get(T + "/overdue", timeout=10)
    assert len(r.json()["tasks"]) <= 20


def test_overdue_invalid_page_size():
    r = requests.get(T + "/overdue", params={"page_size": "abc"}, timeout=10)
    assert r.status_code == 400


def test_assign_ok(data):
    t = data["old"]
    r = requests.post(f"{T}/{t['id']}/assign", json={"email": "alice@example.com"}, headers=H, timeout=10)
    assert r.status_code == 200
    j = r.json()
    assert j["id"] == t["id"] and j["assigned_to"] == "alice@example.com"
    assert j["updated_at"] >= t["updated_at"]
    g = requests.get(f"{T}/{t['id']}", timeout=10).json()
    assert g["assigned_to"] == "alice@example.com"


@pytest.mark.parametrize("body", [{"email": "not-an-email"}, {"email": ""}, {}])
def test_assign_invalid_email(data, body):
    r = requests.post(f"{T}/{data['future']['id']}/assign", json=body, headers=H, timeout=10)
    assert r.status_code == 400


def test_assign_unknown_task():
    r = requests.post(f"{T}/does-not-exist/assign", json={"email": "a@b.com"}, headers=H, timeout=10)
    assert r.status_code == 404


def test_existing_routes(data):
    assert requests.get(T, timeout=10).status_code == 200
    assert requests.get(T + "/stats", timeout=10).status_code == 200
    assert requests.get(T + "/search", params={"q": RUN}, timeout=10).status_code == 200
    assert requests.get(f"{T}/{data['old']['id']}", timeout=10).status_code == 200
    assert requests.get(T + "/nonexistent-id", timeout=10).status_code == 404
