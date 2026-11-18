package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

type fakeNotifier struct {
	calls []string
	err   error
}

func (f *fakeNotifier) NotifyAssigned(task models.Task, email string) error {
	f.calls = append(f.calls, task.ID+":"+email)
	return f.err
}

var fixedNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func newTestService(n Notifier) *TaskService {
	s := NewTaskService(WithClock(func() time.Time { return fixedNow }), WithNotifier(n))
	s.tasks = map[string]*models.Task{}
	return s
}

func addTask(t *testing.T, s *TaskService, title string, due *time.Time, status models.TaskStatus) *models.Task {
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

func TestListOverdueTasks(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	addTask(t, s, "late-2d", at(-48*time.Hour), models.TaskStatusPending)
	addTask(t, s, "late-1h", at(-time.Hour), models.TaskStatusInProgress)
	addTask(t, s, "future", at(time.Hour), models.TaskStatusPending)
	addTask(t, s, "no-due", nil, models.TaskStatusPending)
	addTask(t, s, "done", at(-time.Hour), models.TaskStatusCompleted)
	addTask(t, s, "cancelled", at(-time.Hour), models.TaskStatusCancelled)
	addTask(t, s, "on-hold", at(-72*time.Hour), models.TaskStatusOnHold)

	resp := s.ListOverdueTasks(models.OverdueTasksQuery{})
	var got []string
	for _, tk := range resp.Tasks {
		got = append(got, tk.Title)
	}
	want := []string{"on-hold", "late-2d", "late-1h"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if resp.TotalCount != 3 || resp.NextPageToken != "" {
		t.Fatalf("unexpected totals: %+v", resp)
	}
}

func TestListOverdueTasksTieBreakAndPagination(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	due := at(-time.Hour)
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), due, models.TaskStatusPending)
	}

	tests := []struct {
		name      string
		query     models.OverdueTasksQuery
		wantLen   int
		wantToken string
	}{
		{"first page", models.OverdueTasksQuery{PageSize: 2}, 2, "2"},
		{"second page", models.OverdueTasksQuery{PageSize: 2, PageToken: "2"}, 2, "4"},
		{"last page", models.OverdueTasksQuery{PageSize: 2, PageToken: "4"}, 1, ""},
		{"past end", models.OverdueTasksQuery{PageSize: 2, PageToken: "10"}, 0, ""},
		{"default size", models.OverdueTasksQuery{}, 5, ""},
		{"capped size", models.OverdueTasksQuery{PageSize: 1000}, 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := s.ListOverdueTasks(tt.query)
			if len(r.Tasks) != tt.wantLen || r.NextPageToken != tt.wantToken || r.TotalCount != 5 {
				t.Fatalf("got len=%d token=%q total=%d", len(r.Tasks), r.NextPageToken, r.TotalCount)
			}
		})
	}

	// Ordering is deterministic across calls.
	a := s.ListOverdueTasks(models.OverdueTasksQuery{})
	b := s.ListOverdueTasks(models.OverdueTasksQuery{})
	for i := range a.Tasks {
		if a.Tasks[i].ID != b.Tasks[i].ID {
			t.Fatal("ordering not deterministic")
		}
	}
}

func TestListTasksPaginationRegression(t *testing.T) {
	s := newTestService(&fakeNotifier{})
	for i := 0; i < 5; i++ {
		addTask(t, s, fmt.Sprintf("t%d", i), nil, models.TaskStatusPending)
	}
	r, err := s.ListTasks(models.ListTasksQuery{PageSize: 2, SortOrder: models.SortOrderCreatedAtAsc})
	if err != nil || len(r.Tasks) != 2 || r.NextPageToken != "2" || r.TotalCount != 5 {
		t.Fatalf("unexpected: %+v %v", r, err)
	}
}

func TestAssignTask(t *testing.T) {
	n := &fakeNotifier{}
	s := newTestService(n)
	task := addTask(t, s, "x", nil, models.TaskStatusPending)

	got, err := s.AssignTask(task.ID, "a@example.com")
	if err != nil || got.AssignedTo == nil || *got.AssignedTo != "a@example.com" {
		t.Fatalf("unexpected: %+v %v", got, err)
	}
	if len(n.calls) != 1 || n.calls[0] != task.ID+":a@example.com" {
		t.Fatalf("notifier calls: %v", n.calls)
	}
	if !got.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("updated_at not set from clock")
	}

	if _, err := s.AssignTask("missing", "a@example.com"); err == nil {
		t.Fatal("expected not found")
	}
	if len(n.calls) != 1 {
		t.Fatal("notifier called for unknown task")
	}

	// Notifier failure does not fail the assignment.
	n.err = fmt.Errorf("boom")
	if _, err := s.AssignTask(task.ID, "b@example.com"); err != nil {
		t.Fatalf("notifier error leaked: %v", err)
	}
}
