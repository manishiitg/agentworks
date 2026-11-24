package server

import (
	"strconv"
	"testing"
)

// The shared Project tasks list keeps every open task however many finished
// ones pile up, and a step outside the project's list is refused.
func TestProjectTasksKeepOpenWorkAndOwnSteps(t *testing.T) {
	list := projectTaskList{Steps: []string{"New", "Doing", "Done"}}
	list.Tasks = append(list.Tasks, projectTask{ID: "open-old", Step: "Doing"})
	for i := 0; i < projectTasksDoneKept+10; i++ {
		list.Tasks = append(list.Tasks, projectTask{ID: "done-" + strconv.Itoa(i), Step: "done"})
	}
	list.Tasks = append(list.Tasks, projectTask{ID: "open-new"})
	kept := projectTasksTrim(list)
	if len(kept.Tasks) != projectTasksDoneKept+2 || kept.Tasks[0].ID != "open-old" || kept.Tasks[len(kept.Tasks)-1].ID != "open-new" {
		t.Fatalf("kept %d tasks, first %q last %q", len(kept.Tasks), kept.Tasks[0].ID, kept.Tasks[len(kept.Tasks)-1].ID)
	}
	if kept.stepOf(kept.Tasks[len(kept.Tasks)-1]) != "New" {
		t.Fatalf("a task without a step reads as the first step")
	}
	if _, err := projectTasksStep(list, "Review"); err == nil {
		t.Fatal("a step outside the project's steps was accepted")
	}
	if step, err := projectTasksStep(list, "doing"); err != nil || step != "Doing" {
		t.Fatalf("step match: %q %v", step, err)
	}
}

// Needs you is decided by the server: a proposal, an open question, an ask stuck
// on the person and an unassigned task all show there, a held one does not.
func TestProjectTasksNeedsYou(t *testing.T) {
	list := projectTaskList{Steps: []string{"New", "Merged", "Done"}}
	cases := []struct {
		task projectTask
		want bool
	}{
		{projectTask{Assignee: "Docs agent"}, false},
		{projectTask{}, true},
		{projectTask{Proposed: "Docs agent"}, true},
		{projectTask{Assignee: "Docs agent", Waiting: "Which server first?"}, true},
		{projectTask{Held: true}, false},
		{projectTask{Step: "Done"}, false},
		{projectTask{Step: "Merged"}, false}, // past the first step: nobody needs to hand it out
	}
	for i, c := range cases {
		if got := projectTaskView(list, c.task, nil).NeedsYou != ""; got != c.want {
			t.Errorf("case %d: needs you = %v, want %v", i, got, c.want)
		}
	}
}
