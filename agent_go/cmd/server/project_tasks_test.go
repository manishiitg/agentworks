package server

import (
	"context"
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
	// The person's answer buttons come only with an open question.
	if v := projectTaskView(list, projectTask{Waiting: "Fix now or later?", Choices: []string{"Fix now", "Later"}}, nil); len(v.Choices) != 2 {
		t.Errorf("choices = %v", v.Choices)
	}
	if _, err := projectTaskChoices([]interface{}{"a", "b", "c", "d", "e"}); err == nil {
		t.Error("five choices: want an error")
	}
}

func TestProjectTaskChecklist(t *testing.T) {
	task := projectTask{}
	if changed, err := projectTaskChecklist(&task, map[string]interface{}{"checklist_add": []interface{}{"Server field", "Panel list", "Skill line"}}, "Docs agent", 1); err != nil || !changed || len(task.Checks) != 3 {
		t.Fatalf("add: changed=%v err=%v checks=%v", changed, err, task.Checks)
	}
	if _, err := projectTaskChecklist(&task, map[string]interface{}{"checklist_done": float64(2)}, "Docs agent", 2); err != nil || !task.Checks[1].Done {
		t.Fatalf("done: err=%v checks=%v", err, task.Checks)
	}
	if changed, _ := projectTaskChecklist(&task, map[string]interface{}{"checklist_done": float64(2)}, "Docs agent", 3); changed {
		t.Fatal("ticking a ticked item again should change nothing")
	}
	if _, err := projectTaskChecklist(&task, map[string]interface{}{"checklist_open": float64(2), "checklist_remove": float64(1)}, "Docs agent", 4); err != nil || len(task.Checks) != 2 || task.Checks[0].Done || task.Checks[0].Text != "Panel list" {
		t.Fatalf("open and remove: err=%v checks=%v", err, task.Checks)
	}
	for _, bad := range []map[string]interface{}{{"checklist_done": float64(9)}, {"checklist_done": 1.5}, {"checklist_remove": "one"}, {"checklist_add": []interface{}{" "}}} {
		if _, err := projectTaskChecklist(&task, bad, "", 5); err == nil {
			t.Fatalf("%v: want an error", bad)
		}
	}
	if view := projectTaskView(projectTaskList{}, task, nil); len(view.Checks) != 2 {
		t.Fatalf("view checklist = %v", view.Checks)
	}
}

// The lead's groups decide where a task shows: a task in a group the lead later
// removed falls back to Other instead of vanishing, an unknown group is refused,
// and past-due work shows as overdue until it is done.
func TestProjectTaskGroupsAndDue(t *testing.T) {
	list := projectTaskList{Steps: []string{"New", "Doing", "Done"}, Groups: []projectTaskGroup{{Name: "Billing"}}}
	task := projectTask{ID: "t1", Step: "Doing"}
	if _, err := projectTaskPlace(list, &task, map[string]interface{}{"group": "billing", "due": "2000-01-02", "pinned": true}); err != nil || task.Group != "Billing" || !task.Pinned {
		t.Fatalf("place: %+v %v", task, err)
	}
	if _, err := projectTaskPlace(list, &task, map[string]interface{}{"group": "Marketing"}); err == nil {
		t.Fatal("a group the lead never made was accepted")
	}
	if _, err := projectTaskPlace(list, &task, map[string]interface{}{"due": "next friday"}); err == nil {
		t.Fatal("a due date that isn't a date was accepted")
	}
	tasks := conditionFeedTaskIndex(context.Background(), "")
	if v := projectTaskView(list, task, tasks); v.Group != "Billing" || !v.Overdue {
		t.Fatalf("view: group %q overdue %v", v.Group, v.Overdue)
	}
	list.Groups = nil
	task.Step = "Done"
	if v := projectTaskView(list, task, tasks); v.Group != "" || v.Overdue {
		t.Fatalf("removed group or done work: group %q overdue %v", v.Group, v.Overdue)
	}
	if _, err := projectTaskGroups([]interface{}{map[string]interface{}{"name": "Other"}}); err == nil {
		t.Fatal("a group named Other was accepted")
	}
}
