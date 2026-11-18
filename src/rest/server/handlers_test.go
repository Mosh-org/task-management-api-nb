package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
	"github.com/gin-gonic/gin"
)

var fixedNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct{ emails []string }

func (f *fakeNotifier) NotifyAssigned(task models.Task, email string) {
	f.emails = append(f.emails, email)
}

func setup(t *testing.T) (*gin.Engine, *services.TaskService, *fakeNotifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	n := &fakeNotifier{}
	svc := services.NewTaskService(
		services.WithClock(func() time.Time { return fixedNow }),
		services.WithNotifier(n),
	)
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r)
	return r, svc, n
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func createTask(t *testing.T, svc *services.TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		if _, err := svc.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func ago(d time.Duration) *time.Time {
	v := fixedNow.Add(-d)
	return &v
}

func TestOverdueEndpoint(t *testing.T) {
	r, svc, _ := setup(t)
	old := createTask(t, svc, "old", ago(48*time.Hour), "")
	recent := createTask(t, svc, "recent", ago(time.Hour), models.TaskStatusInProgress)
	createTask(t, svc, "done", ago(72*time.Hour), models.TaskStatusCompleted)
	createTask(t, svc, "cancelled", ago(72*time.Hour), models.TaskStatusCancelled)
	future := fixedNow.Add(time.Hour)
	createTask(t, svc, "future", &future, "")

	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp models.ListTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	if resp.Tasks[0].ID != old.ID || resp.Tasks[1].ID != recent.ID {
		t.Fatalf("wrong order: %s", w.Body.String())
	}
}

func TestOverdueRoutePrecedence(t *testing.T) {
	r, _, _ := setup(t)
	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	// If /:id captured the request we would see a NOT_FOUND error body.
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var m map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &m)
	if _, ok := m["tasks"]; !ok {
		t.Fatalf("expected list response, got %s", w.Body.String())
	}
}

func TestOverdueEndpointPagination(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantCode int
		wantLen  int
		wantNext string
	}{
		{"first page", "?page_size=2", 200, 2, "2"},
		{"second page", "?page_size=2&page_token=2", 200, 2, "4"},
		{"last page", "?page_size=2&page_token=4", 200, 1, ""},
		{"default size", "", 200, 5, ""},
		{"page size zero", "?page_size=0", 400, 0, ""},
		{"page size too large", "?page_size=101", 400, 0, ""},
		{"page size not a number", "?page_size=abc", 400, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, svc, _ := setup(t)
			for i := 1; i <= 5; i++ {
				createTask(t, svc, "t", ago(time.Duration(i)*time.Hour), "")
			}
			w := do(r, http.MethodGet, "/api/v1/tasks/overdue"+tt.query, "")
			if w.Code != tt.wantCode {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tt.wantCode != 200 {
				var e models.ErrorResponse
				json.Unmarshal(w.Body.Bytes(), &e)
				if e.Error.Code != "VALIDATION_ERROR" {
					t.Fatalf("error code %q", e.Error.Code)
				}
				return
			}
			var resp models.ListTasksResponse
			json.Unmarshal(w.Body.Bytes(), &resp)
			if len(resp.Tasks) != tt.wantLen || resp.NextPageToken != tt.wantNext || resp.TotalCount != 5 {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
		})
	}
}

func TestAssignEndpoint(t *testing.T) {
	r, svc, n := setup(t)
	task := createTask(t, svc, "assignable", nil, "")

	tests := []struct {
		name     string
		id       string
		body     string
		wantCode int
		wantErr  string
	}{
		{"success", task.ID, `{"email":"bob@example.com"}`, 200, ""},
		{"invalid email", task.ID, `{"email":"not-an-email"}`, 400, "VALIDATION_ERROR"},
		{"missing email", task.ID, `{}`, 400, "VALIDATION_ERROR"},
		{"empty email", task.ID, `{"email":""}`, 400, "VALIDATION_ERROR"},
		{"malformed json", task.ID, `{`, 400, "VALIDATION_ERROR"},
		{"unknown task", "missing-id", `{"email":"bob@example.com"}`, 404, "NOT_FOUND"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(n.emails)
			w := do(r, http.MethodPost, "/api/v1/tasks/"+tt.id+"/assign", tt.body)
			if w.Code != tt.wantCode {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tt.wantErr == "" {
				var got models.Task
				json.Unmarshal(w.Body.Bytes(), &got)
				if got.ID != tt.id || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
					t.Fatalf("unexpected task: %s", w.Body.String())
				}
				if len(n.emails) != before+1 {
					t.Fatal("notifier not called")
				}
				return
			}
			var e models.ErrorResponse
			json.Unmarshal(w.Body.Bytes(), &e)
			if e.Error.Code != tt.wantErr {
				t.Fatalf("error code %q", e.Error.Code)
			}
			if len(n.emails) != before {
				t.Fatal("notifier must not be called on failure")
			}
		})
	}
}

func TestExistingRoutesStillWork(t *testing.T) {
	r, svc, _ := setup(t)
	task := createTask(t, svc, "x", nil, "")
	for _, path := range []string{"/api/v1/tasks", "/api/v1/tasks/stats", "/api/v1/tasks/search?q=x", "/api/v1/tasks/" + task.ID} {
		if w := do(r, http.MethodGet, path, ""); w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, w.Code)
		}
	}
}
