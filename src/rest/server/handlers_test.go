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

type fakeNotifier struct{ calls []string }

func (f *fakeNotifier) NotifyAssigned(task models.Task, email string) error {
	f.calls = append(f.calls, task.ID+":"+email)
	return nil
}

var fixedNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func setup() (*gin.Engine, *services.TaskService, *fakeNotifier) {
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
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func createTask(t *testing.T, svc *services.TaskService, title string, due time.Time) *models.Task {
	t.Helper()
	task, err := svc.CreateTask(models.CreateTaskRequest{Title: title, DueDate: &due})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestOverdueEndpoint(t *testing.T) {
	r, svc, _ := setup()
	createTask(t, svc, "late", fixedNow.Add(-time.Hour))
	createTask(t, svc, "later", fixedNow.Add(-2*time.Hour))
	createTask(t, svc, "future", fixedNow.Add(time.Hour))
	done := createTask(t, svc, "done", fixedNow.Add(-time.Hour))
	if _, err := svc.UpdateTaskStatus(done.ID, models.TaskStatusCompleted); err != nil {
		t.Fatal(err)
	}

	// Route precedence: /overdue must not be captured by /:id.
	w := do(r, http.MethodGet, "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	var resp models.OverdueTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 || resp.Tasks[0].Title != "later" || resp.Tasks[1].Title != "late" {
		t.Fatalf("unexpected response: %s", w.Body)
	}

	// Pagination
	w = do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=1", "")
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 1 || resp.NextPageToken != "1" {
		t.Fatalf("unexpected page: %s", w.Body)
	}
	w = do(r, http.MethodGet, "/api/v1/tasks/overdue?page_size=1&page_token=1", "")
	resp = models.OverdueTasksResponse{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Tasks) != 1 || resp.Tasks[0].Title != "late" || resp.NextPageToken != "" {
		t.Fatalf("unexpected page 2: %s", w.Body)
	}

	// Validation
	for _, q := range []string{"page_size=0", "page_size=101", "page_size=abc"} {
		if w := do(r, http.MethodGet, "/api/v1/tasks/overdue?"+q, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", q, w.Code)
		}
	}
}

func TestAssignEndpoint(t *testing.T) {
	r, svc, n := setup()
	task := createTask(t, svc, "t", fixedNow)

	tests := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{"success", "/api/v1/tasks/" + task.ID + "/assign", `{"email":"a@example.com"}`, http.StatusOK},
		{"invalid email", "/api/v1/tasks/" + task.ID + "/assign", `{"email":"nope"}`, http.StatusBadRequest},
		{"missing email", "/api/v1/tasks/" + task.ID + "/assign", `{}`, http.StatusBadRequest},
		{"bad json", "/api/v1/tasks/" + task.ID + "/assign", `{`, http.StatusBadRequest},
		{"unknown task", "/api/v1/tasks/missing/assign", `{"email":"a@example.com"}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(r, http.MethodPost, tt.path, tt.body)
			if w.Code != tt.status {
				t.Fatalf("status %d want %d body %s", w.Code, tt.status, w.Body)
			}
			if tt.status == http.StatusOK {
				var got models.Task
				_ = json.Unmarshal(w.Body.Bytes(), &got)
				if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "a@example.com" {
					t.Fatalf("unexpected task: %s", w.Body)
				}
			} else {
				var e models.ErrorResponse
				_ = json.Unmarshal(w.Body.Bytes(), &e)
				if e.Error.Code == "" {
					t.Fatalf("no error code: %s", w.Body)
				}
			}
		})
	}
	if len(n.calls) != 1 {
		t.Fatalf("notifier calls: %v", n.calls)
	}
}
