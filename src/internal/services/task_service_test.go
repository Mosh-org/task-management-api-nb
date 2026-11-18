package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

var fixedNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	calls []string
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, email string) {
	f.calls = append(f.calls, task.ID+"|"+email)
}

// newEmptyService returns a service with no sample data, a fixed clock and a fake notifier.
func newEmptyService() (*TaskService, *fakeNotifier) {
	n := &fakeNotifier{}
	s := NewTaskService(WithClock(func() time.Time { return fixedNow }), WithNotifier(n))
	s.tasks = map[string]*models.Task{}
	return s, n
}

func addTask(t *testing.T, s *TaskService, title string, status models.TaskStatus, due *time.Time) *models.Task {
	t.Helper()
	task, err := s.CreateTask(models.CreateTaskRequest{Title: title, DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	task.Status = status
	return task
}

func at(d time.Duration) *time.Time {
	v := fixedNow.Add(d)
	return &v
}

func TestListOverdueTasks_Filtering(t *testing.T) {
	tests := []struct {
		name   string
		status models.TaskStatus
		due    *time.Time
		want   bool
	}{
		{"past pending", models.TaskStatusPending, at(-time.Hour), true},
		{"past in progress", models.TaskStatusInProgress, at(-time.Hour), true},
		{"past on hold", models.TaskStatusOnHold, at(-time.Hour), true},
		{"past completed", models.TaskStatusCompleted, at(-time.Hour), false},
		{"past cancelled", models.TaskStatusCancelled, at(-time.Hour), false},
		{"future pending", models.TaskStatusPending, at(time.Hour), false},
		{"due exactly now", models.TaskStatusPending, at(0), false},
		{"no due date", models.TaskStatusPending, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newEmptyService()
			addTask(t, s, tt.name, tt.status, tt.due)
			resp := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
			if got := resp.TotalCount == 1 && len(resp.Tasks) == 1; got != tt.want {
				t.Fatalf("overdue = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListOverdueTasks_EmptyIsNonNilSlice(t *testing.T) {
	s, _ := newEmptyService()
	resp := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
	if resp.Tasks == nil || len(resp.Tasks) != 0 || resp.TotalCount != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestListOverdueTasks_OrderAndTieBreak(t *testing.T) {
	s, _ := newEmptyService()
	later := addTask(t, s, "later", models.TaskStatusPending, at(-time.Hour))
	earliest := addTask(t, s, "earliest", models.TaskStatusPending, at(-48*time.Hour))
	tieA := addTask(t, s, "tieA", models.TaskStatusPending, at(-24*time.Hour))
	tieB := addTask(t, s, "tieB", models.TaskStatusPending, at(-24*time.Hour))
	// Force identical CreatedAt so the ID decides.
	tieA.CreatedAt, tieB.CreatedAt = fixedNow, fixedNow
	tieFirst, tieSecond := tieA, tieB
	if tieB.ID < tieA.ID {
		tieFirst, tieSecond = tieB, tieA
	}

	want := []string{earliest.ID, tieFirst.ID, tieSecond.ID, later.ID}
	for i := 0; i < 20; i++ { // map iteration is random; result must be stable
		resp := s.ListOverdueTasks(models.ListOverdueTasksQuery{})
		for j, id := range want {
			if resp.Tasks[j].ID != id {
				t.Fatalf("iteration %d position %d: got %s want %s", i, j, resp.Tasks[j].ID, id)
			}
		}
	}
}

func TestListOverdueTasks_Pagination(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		pageSize  int
		token     string
		wantLen   int
		wantNext  string
		wantTotal int
	}{
		{"default page size", 25, 0, "", 20, "20", 25},
		{"second page", 25, 20, "20", 5, "", 25},
		{"small page", 5, 2, "", 2, "2", 5},
		{"middle page", 5, 2, "2", 2, "4", 5},
		{"last page", 5, 2, "4", 1, "", 5},
		{"page size capped at 100", 120, 500, "", 100, "100", 120},
		{"token beyond end", 3, 2, "10", 0, "", 3},
		{"invalid token starts at 0", 3, 2, "abc", 2, "2", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newEmptyService()
			for i := 0; i < tt.total; i++ {
				addTask(t, s, fmt.Sprintf("t%d", i), models.TaskStatusPending, at(-time.Duration(i+1)*time.Hour))
			}
			resp := s.ListOverdueTasks(models.ListOverdueTasksQuery{PageSize: tt.pageSize, PageToken: tt.token})
			if len(resp.Tasks) != tt.wantLen || resp.NextPageToken != tt.wantNext || resp.TotalCount != tt.wantTotal {
				t.Fatalf("got len=%d next=%q total=%d; want len=%d next=%q total=%d",
					len(resp.Tasks), resp.NextPageToken, resp.TotalCount, tt.wantLen, tt.wantNext, tt.wantTotal)
			}
		})
	}
}

// ListTasks regression: shared pagination must keep its behavior.
func TestListTasks_PaginationUnchanged(t *testing.T) {
	tests := []struct {
		name     string
		pageSize int
		token    string
		wantLen  int
		wantNext string
	}{
		{"default", 0, "", 7, ""},
		{"first page of 3", 3, "", 3, "3"},
		{"second page of 3", 3, "3", 3, "6"},
		{"last page", 3, "6", 1, ""},
		{"beyond end", 3, "50", 0, ""},
		{"capped", 1000, "", 7, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newEmptyService()
			for i := 0; i < 7; i++ {
				addTask(t, s, fmt.Sprintf("t%d", i), models.TaskStatusPending, nil)
			}
			resp, err := s.ListTasks(models.ListTasksQuery{PageSize: tt.pageSize, PageToken: tt.token})
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Tasks) != tt.wantLen || resp.NextPageToken != tt.wantNext || resp.TotalCount != 7 {
				t.Fatalf("got len=%d next=%q total=%d", len(resp.Tasks), resp.NextPageToken, resp.TotalCount)
			}
		})
	}
}

func TestAssignTask(t *testing.T) {
	t.Run("assigns and notifies", func(t *testing.T) {
		s, n := newEmptyService()
		task := addTask(t, s, "a", models.TaskStatusPending, nil)
		got, err := s.AssignTask(task.ID, "bob@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" {
			t.Fatalf("assigned_to = %v", got.AssignedTo)
		}
		if !got.UpdatedAt.Equal(fixedNow) {
			t.Fatalf("updated_at = %v, want %v", got.UpdatedAt, fixedNow)
		}
		stored, _ := s.GetTask(task.ID)
		if stored.AssignedTo == nil || *stored.AssignedTo != "bob@example.com" {
			t.Fatal("assignment not persisted")
		}
		if len(n.calls) != 1 || n.calls[0] != task.ID+"|bob@example.com" {
			t.Fatalf("notifier calls = %v", n.calls)
		}
	})
	t.Run("reassign notifies again", func(t *testing.T) {
		s, n := newEmptyService()
		task := addTask(t, s, "a", models.TaskStatusPending, nil)
		s.AssignTask(task.ID, "a@example.com")
		got, _ := s.AssignTask(task.ID, "b@example.com")
		if *got.AssignedTo != "b@example.com" || len(n.calls) != 2 {
			t.Fatalf("got %v calls %v", *got.AssignedTo, n.calls)
		}
	})
	t.Run("unknown task", func(t *testing.T) {
		s, n := newEmptyService()
		_, err := s.AssignTask("missing", "bob@example.com")
		if err == nil || err.Error() != "task with ID missing not found" {
			t.Fatalf("err = %v", err)
		}
		if len(n.calls) != 0 {
			t.Fatal("notifier must not be called for unknown task")
		}
	})
}

func TestDefaultNotifierDoesNotPanic(t *testing.T) {
	s := NewTaskService()
	task, _ := s.CreateTask(models.CreateTaskRequest{Title: "x"})
	if _, err := s.AssignTask(task.ID, "bob@example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestListTasks_DefaultOrderStableAcrossPages(t *testing.T) {
	s, _ := newEmptyService()
	for i := 0; i < 10; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), models.TaskStatusPending, nil)
	}
	for run := 0; run < 10; run++ {
		seen := map[string]bool{}
		token := ""
		for {
			resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 3, PageToken: token})
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range resp.Tasks {
				if seen[task.ID] {
					t.Fatalf("run %d: duplicate task %s across pages", run, task.ID)
				}
				seen[task.ID] = true
			}
			if resp.NextPageToken == "" {
				break
			}
			token = resp.NextPageToken
		}
		if len(seen) != 10 {
			t.Fatalf("run %d: saw %d tasks, want 10", run, len(seen))
		}
	}
}
