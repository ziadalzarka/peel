package store

import "testing"

func remoteNote(id string, line int, body, author string, resolved bool) Comment {
	return Comment{ID: id, File: "a.go", Line: line, Body: body, Author: Author(author), Resolved: resolved, Remote: "github"}
}

func mustImport(t *testing.T, s *ReviewStore, remote ...Comment) {
	t.Helper()
	if err := s.Import(remote); err != nil {
		t.Fatalf("Import: %v", err)
	}
}

func storedByID(t *testing.T, s *ReviewStore) map[string]Comment {
	t.Helper()
	all, err := s.Comments().List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	out := map[string]Comment{}
	for _, c := range all {
		out[c.ID] = c
	}
	return out
}

func TestImportAddsTheForgesCommentsOnce(t *testing.T) {
	s := newTestReview(t)
	remote := []Comment{remoteNote("github-1", 3, "why", "octocat", false), remoteNote("github-2", 5, "done", "hubot", true)}

	mustImport(t, s, remote...)
	mustImport(t, s, remote...)

	got := storedByID(t, s)
	if len(got) != 2 {
		t.Fatalf("stored %d comments, want 2: %+v", len(got), got)
	}
	if c := got["github-1"]; c.Author != "octocat" || c.Resolved || c.Target != testTarget || c.Remote != "github" {
		t.Errorf("github-1 = %+v", c)
	}
	if !got["github-2"].Resolved {
		t.Error("a thread resolved on the forge came in open")
	}
}

func TestImportDoesNotBringBackACommentDeletedHere(t *testing.T) {
	s := newTestReview(t)
	remote := []Comment{remoteNote("github-1", 3, "why", "octocat", false), remoteNote("github-2", 5, "and this", "hubot", false)}
	mustImport(t, s, remote...)
	if err := s.Comments().Remove("github-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	mustImport(t, s, remote...)

	if got := storedByID(t, s); len(got) != 1 || got["github-2"].ID == "" {
		t.Errorf("stored %+v, want only github-2 — github-1 was deleted here", got)
	}
}

func TestImportFollowsAResolveOnTheForgeAndKeepsOneMadeHere(t *testing.T) {
	s := newTestReview(t)
	mustImport(t, s, remoteNote("github-1", 3, "why", "octocat", false), remoteNote("github-2", 5, "and this", "hubot", false))
	if _, err := s.Comments().Update("github-1", func(c *Comment) { c.Resolved = true }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	mustImport(t, s, remoteNote("github-1", 3, "why", "octocat", false), remoteNote("github-2", 5, "and this", "hubot", true))

	got := storedByID(t, s)
	if !got["github-1"].Resolved {
		t.Error("a resolve made here was undone by a forge that had not changed")
	}
	if !got["github-2"].Resolved {
		t.Error("a thread resolved on the forge since the last import is still open here")
	}
}

func TestImportDropsACommentGoneFromTheForgeAndKeepsLocalNotes(t *testing.T) {
	s := newTestReview(t)
	mine, err := s.Comments().Add(Comment{File: "a.go", Line: 1, Body: "mine"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	mustImport(t, s, remoteNote("github-1", 3, "why", "octocat", false), remoteNote("github-2", 5, "and this", "hubot", false))

	mustImport(t, s, remoteNote("github-2", 5, "and this", "hubot", false))

	got := storedByID(t, s)
	if _, ok := got["github-1"]; ok {
		t.Error("a comment gone from the forge, or outdated there, is still here")
	}
	if _, ok := got[mine.ID]; !ok || len(got) != 2 {
		t.Errorf("stored %+v, want the local note and github-2", got)
	}
}

func TestImportSkipsACommentThatWasPostedFromHere(t *testing.T) {
	s := newTestReview(t)
	posted, err := s.Comments().Add(Comment{File: "a.go", Line: 2, EndLine: 4, Body: "posted from peel", Resolved: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	mustImport(t, s, remoteNote("github-9", 2, "posted from peel\n", "ziadalzarka", false))

	if got := storedByID(t, s); len(got) != 1 || got[posted.ID].ID == "" {
		t.Errorf("stored %+v, want only the note posted from here", got)
	}
}
