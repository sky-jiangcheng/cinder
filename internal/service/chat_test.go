package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"repotory/internal/db"
)

func chatID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

// fixturePath turns a slash-separated fixture path into an absolute path that
// this OS's validator accepts. On Windows a rootless `\metrics\single` is only
// rooted, not absolute — filepath.IsAbs wants a volume — so the synthetic one is
// supplied here. Nothing touches the filesystem: these paths are stored labels.
// An empty fixture stays empty, because empty means unscoped rather than root.
func fixturePath(slash string) string {
	if slash == "" || filepath.Separator == '/' {
		return slash
	}
	return `C:\` + filepath.FromSlash(strings.TrimPrefix(slash, "/"))
}

func sharedChatServices(t *testing.T) (*Service, *Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	open := func() *Service {
		d, err := db.InitDB(path)
		if err != nil {
			t.Fatal(err)
		}
		s := NewWithDeps(d, &fakeGit{}, nil, "tester")
		t.Cleanup(func() { s.Shutdown(); _ = d.Close() })
		return s
	}
	return open(), open(), path
}

func chatExec(t *testing.T, d *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := d.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestChatSharedPersistenceAndIsolation(t *testing.T) {
	a, b, path := sharedChatServices(t)
	ctx := context.Background()
	thread, err := a.CreateChatThread(ctx, chatID(1), "A conversation", fixturePath("/workspace/repo"))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := b.ListChatThreads(ctx, "")
	if err != nil || len(listed) != 1 || listed[0] != *thread {
		t.Fatalf("cross-connection list: %+v %v", listed, err)
	}
	empty, err := b.ReadChatThread(ctx, thread.ID)
	if err != nil || empty.Messages == nil || len(empty.Messages) != 0 {
		t.Fatalf("empty read: %+v %v", empty, err)
	}
	user := ChatAppendInput{ThreadID: thread.ID, ID: chatID(2), Role: "user", Content: "isolatedChatNeedle\n  preserve whitespace  ", Source: "warden"}
	u, err := a.AppendChatMessage(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	assistant := ChatAppendInput{ThreadID: thread.ID, ID: u.ID + "-reply", Role: "assistant", Content: "An answer", Source: "vscode", ReplyTo: &u.ID}
	m, err := b.AppendChatMessage(ctx, assistant)
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadChatThread(ctx, thread.ID)
	if err != nil || !reflect.DeepEqual(read.Messages, []ChatMessage{*u, *m}) {
		t.Fatalf("shared messages: %+v %v", read, err)
	}
	for _, stamp := range []string{thread.CreatedAt, read.Thread.UpdatedAt, u.CreatedAt, m.CreatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if read.Thread.UpdatedAt <= thread.UpdatedAt {
		t.Fatal("append did not advance thread timestamp")
	}
	for _, in := range []ChatAppendInput{user, assistant} {
		if _, err := b.AppendChatMessage(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	again, err := b.ReadChatThread(ctx, thread.ID)
	if err != nil || !reflect.DeepEqual(read, again) {
		t.Fatalf("retry changed persisted values: %+v %v", again, err)
	}
	renamed, err := b.RenameChatThread(ctx, thread.ID, "New title")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := a.CreateChatThread(ctx, thread.ID, thread.Title, thread.Path)
	if err != nil || *retry != *renamed {
		t.Fatalf("create retry after rename: %+v %v", retry, err)
	}
	for _, input := range [][2]string{{"different", thread.Path}, {thread.Title, fixturePath("/another")}} {
		if _, err := b.CreateChatThread(ctx, thread.ID, input[0], input[1]); !errors.Is(err, ErrChatConflict) {
			t.Fatalf("create conflict: %v", err)
		}
	}
	if got, err := b.ListChatThreads(ctx, fixturePath("/workspace/repo-neighbor")); err != nil || len(got) != 0 {
		t.Fatalf("exact path filter: %v %v", got, err)
	}
	if got, err := b.ListChatThreads(ctx, thread.Path); err != nil || len(got) != 1 || got[0].Title != renamed.Title {
		t.Fatalf("rename visibility: %v %v", got, err)
	}
	for _, table := range []string{"project_notes", "note_topics", "wiki_pages", "compile_jobs", "note_embed_dirty", "page_embed_dirty", "ai_call_log"} {
		var count int
		if err := a.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("chat polluted %s: %d rows", table, count)
		}
	}
	if hits := a.mustSearchAll("isolatedChatNeedle"); len(hits) != 0 {
		t.Fatalf("chat leaked into search: %v", hits)
	}
	// Close BOTH handles, then reopen: this is persisted data, not Service state.
	_ = a.db.Close()
	_ = b.db.Close()
	d, err := db.InitDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	reopened := NewWithDeps(d, &fakeGit{}, nil, "tester")
	defer reopened.Shutdown()
	stored, err := reopened.ReadChatThread(ctx, thread.ID)
	if err != nil || stored.Thread != *renamed || !reflect.DeepEqual(stored.Messages, read.Messages) {
		t.Fatalf("reopen: %+v %v", stored, err)
	}
}

func TestChatConcurrentIdempotency(t *testing.T) {
	a, b, _ := sharedChatServices(t)
	ctx := context.Background()
	services := []*Service{a, b}
	run := func(f func(*Service) error) {
		t.Helper()
		start := make(chan struct{})
		errs := make(chan error, 24)
		var wg sync.WaitGroup
		for i := 0; i < cap(errs); i++ {
			wg.Add(1)
			go func(s *Service) { defer wg.Done(); <-start; errs <- f(s) }(services[i%2])
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	run(func(s *Service) error { _, err := s.CreateChatThread(ctx, chatID(1), "Concurrent", ""); return err })
	in := ChatAppendInput{ThreadID: chatID(1), ID: chatID(2), Role: "user", Content: "same payload", Source: "jetbrains"}
	run(func(s *Service) error { _, err := s.AppendChatMessage(ctx, in); return err })
	read, err := b.ReadChatThread(ctx, chatID(1))
	if err != nil || len(read.Messages) != 1 {
		t.Fatalf("duplicates under concurrent retry: %+v %v", read, err)
	}
	// Different payloads race for the same id. Exactly one wins; neither can
	// overwrite the other, and subsequent retries still see that winner.
	start, errs := make(chan struct{}), make(chan error, 2)
	for i, s := range services {
		go func(s *Service, content string) {
			<-start
			_, err := s.AppendChatMessage(ctx, ChatAppendInput{ThreadID: in.ThreadID, ID: chatID(3), Role: "user", Content: content, Source: "warden"})
			errs <- err
		}(s, fmt.Sprintf("contender %d", i))
	}
	close(start)
	ok, conflict := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			ok++
		} else if errors.Is(err, ErrChatConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("winners=%d conflicts=%d", ok, conflict)
	}
}

func TestChatConflictsRepliesAndRollback(t *testing.T) {
	a, b, _ := sharedChatServices(t)
	ctx := context.Background()
	for _, n := range []int{1, 2} {
		if _, err := a.CreateChatThread(ctx, chatID(n), "thread", ""); err != nil {
			t.Fatal(err)
		}
	}
	user := ChatAppendInput{ThreadID: chatID(1), ID: chatID(3), Role: "user", Content: "question", Source: "warden"}
	if _, err := a.AppendChatMessage(ctx, user); err != nil {
		t.Fatal(err)
	}
	other := user
	other.ID, other.ThreadID = chatID(4), chatID(2)
	if _, err := b.AppendChatMessage(ctx, other); err != nil {
		t.Fatal(err)
	}
	answer := ChatAppendInput{ThreadID: user.ThreadID, ID: chatID(5), Role: "assistant", Content: "answer", Source: "jetbrains", ReplyTo: &user.ID}
	if _, err := b.AppendChatMessage(ctx, answer); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ChatAppendInput){
		func(i *ChatAppendInput) { i.Content += "!" },
		func(i *ChatAppendInput) { i.Role = "assistant" },
		func(i *ChatAppendInput) { i.Source = "vscode" },
		func(i *ChatAppendInput) { i.ThreadID = chatID(2) },
		func(i *ChatAppendInput) { i.ReplyTo = &other.ID },
	} {
		in := user
		mutate(&in)
		if _, err := b.AppendChatMessage(ctx, in); !errors.Is(err, ErrChatConflict) {
			t.Fatalf("want conflict: %+v: %v", in, err)
		}
	}
	before, err := b.ReadChatThread(ctx, user.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range []string{other.ID, answer.ID, chatID(90), chatID(6)} {
		in := answer
		in.ID, in.ReplyTo = chatID(6), &reply
		if _, err := a.AppendChatMessage(ctx, in); err == nil {
			t.Fatalf("accepted invalid reply_to %s", reply)
		}
	}
	// Failure AFTER inserting the message must roll back both message + stamp.
	chatExec(t, a.db, `CREATE TRIGGER fail_chat_stamp BEFORE UPDATE ON chat_threads
	 WHEN new.updated_at != old.updated_at BEGIN SELECT RAISE(ABORT, 'fixture update failure'); END`)
	in := answer
	in.ID = chatID(7)
	if _, err := a.AppendChatMessage(ctx, in); err == nil {
		t.Fatal("expected transactional failure")
	}
	chatExec(t, a.db, "DROP TRIGGER fail_chat_stamp")
	after, err := b.ReadChatThread(ctx, user.ThreadID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed append changed data: %+v %v", after, err)
	}
}

func TestChatLimitsAndSequenceOrder(t *testing.T) {
	s, _ := setupService(t)
	ctx := context.Background()
	for i := 1; i <= 105; i++ {
		if _, err := s.CreateChatThread(ctx, chatID(i), "thread", fixturePath("/workspace")); err != nil {
			t.Fatal(err)
		}
	}
	// Which five of the 105 the cap evicts is decided by updated_at, and
	// CreateChatThread stamps with the wall clock. On the Windows runner 105
	// creates land inside one clock tick: every row ties, the `id ASC` tie-break
	// returns the lowest ids, and the cap looks broken when it is not. Stamp
	// distinct times so the assertion is about the cap, not the clock.
	for i := 1; i <= 105; i++ {
		chatExec(t, s.db, "UPDATE chat_threads SET updated_at = ? WHERE id = ?",
			fmt.Sprintf("2026-01-01T00:%02d:%02d.000000000Z", i/60, i%60), chatID(i))
	}
	threads, err := s.ListChatThreads(ctx, fixturePath("/workspace"))
	if err != nil || len(threads) != 100 {
		t.Fatalf("bounded threads: %d %v", len(threads), err)
	}
	if threads[0].ID != chatID(105) || threads[99].ID != chatID(6) {
		t.Fatalf("cap kept the wrong end: first %s last %s, want %s then %s", threads[0].ID, threads[99].ID, chatID(105), chatID(6))
	}
	// Deliberately reverse timestamps; ordering must use SQLite seq, not clocks.
	for i := 1; i <= 505; i++ {
		chatExec(t, s.db, "INSERT INTO chat_messages(id, thread_id, role, content, source, created_at) VALUES (?, ?, 'user', ?, 'warden', ?)",
			chatID(1000+i), chatID(1), fmt.Sprint(i), time.Date(2026, 1, 1, 0, 0, 506-i, 0, time.UTC).Format(time.RFC3339))
	}
	read, err := s.ReadChatThread(ctx, chatID(1))
	if err != nil || len(read.Messages) != 500 {
		t.Fatalf("bounded messages: %+v %v", read, err)
	}
	for i, m := range read.Messages {
		if m.ID != chatID(1006+i) {
			t.Fatalf("message %d: %+v", i, m)
		}
	}
}

func TestChatClientIDsAndUnicodeTitles(t *testing.T) {
	s, _ := setupService(t)
	ctx := context.Background()
	// A Chinese title and a supplementary-plane title each use characters,
	// not bytes. Both create and rename enforce the same inclusive boundary.
	for n, character := range []string{"中", "𠮷"} {
		title := strings.Repeat(character, 160)
		thread, err := s.CreateChatThread(ctx, chatID(10+n), title, "")
		if err != nil || thread.Title != title {
			t.Fatalf("Unicode create: %+v %v", thread, err)
		}
		if _, err := s.RenameChatThread(ctx, thread.ID, title); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateChatThread(ctx, chatID(20+n), title+character, ""); err == nil {
			t.Fatal("accepted 161-character title")
		}
		if _, err := s.RenameChatThread(ctx, thread.ID, title+character); err == nil {
			t.Fatal("accepted 161-character rename")
		}
	}
	threadID, userID := chatID(10), chatID(30)
	if _, err := s.AppendChatMessage(ctx, ChatAppendInput{ThreadID: threadID, ID: userID, Role: "user", Content: "question", Source: "warden"}); err != nil {
		t.Fatal(err)
	}
	answer := ChatAppendInput{ThreadID: threadID, ID: userID + "-reply", Role: "assistant", Content: "answer", Source: "vscode", ReplyTo: &userID}
	first, err := s.AppendChatMessage(ctx, answer)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.AppendChatMessage(ctx, answer)
	if err != nil || !reflect.DeepEqual(first, retry) {
		t.Fatalf("reply retry: %+v %v", retry, err)
	}
	for _, bad := range []string{"reply", userID + "-reply-reply", userID + "-REPLY", userID + "-reply ", "bad-reply"} {
		in := answer
		in.ID = bad
		if _, err := s.AppendChatMessage(ctx, in); err == nil {
			t.Fatalf("accepted message id %q", bad)
		}
	}
	// Never extend the suffix exception to thread IDs or reply_to references.
	if _, err := s.CreateChatThread(ctx, answer.ID, "bad thread id", ""); err == nil {
		t.Fatal("create accepted reply suffix")
	}
	if _, err := s.ReadChatThread(ctx, answer.ID); err == nil || errors.Is(err, ErrChatThreadNotFound) {
		t.Fatalf("read did not reject suffix: %v", err)
	}
	if _, err := s.RenameChatThread(ctx, answer.ID, "bad thread id"); err == nil || errors.Is(err, ErrChatThreadNotFound) {
		t.Fatalf("rename did not reject suffix: %v", err)
	}
	in := answer
	in.ThreadID = answer.ID
	if _, err := s.AppendChatMessage(ctx, in); err == nil || errors.Is(err, ErrChatThreadNotFound) {
		t.Fatalf("append did not reject thread suffix: %v", err)
	}
	in = answer
	in.ID, in.ReplyTo = chatID(31), &answer.ID
	if _, err := s.AppendChatMessage(ctx, in); err == nil || !strings.Contains(err.Error(), "reply_to must be a UUID") {
		t.Fatalf("reply_to accepted suffix: %v", err)
	}
}

func TestChatValidationAndStorageErrors(t *testing.T) {
	s, _ := setupService(t)
	ctx := context.Background()
	if _, err := s.CreateChatThread(ctx, chatID(1), "valid", ""); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"", " \t ", "line\nbreak", "bad\x00", string([]byte{0xff}), strings.Repeat("x", MaxChatTitleLen+1)} {
		if _, err := s.CreateChatThread(ctx, chatID(2), title, ""); err == nil {
			t.Fatalf("accepted title %q", title)
		}
		if _, err := s.RenameChatThread(ctx, chatID(1), title); err == nil {
			t.Fatalf("rename accepted title %q", title)
		}
	}
	for _, path := range []string{"relative", "  ", "/bad\x00path", "/bad\npath", "/bad\xff", "/" + strings.Repeat("x", MaxChatPathLen)} {
		if _, err := s.CreateChatThread(ctx, chatID(2), "valid", path); err == nil {
			t.Fatal("accepted invalid create path")
		}
		if _, err := s.ListChatThreads(ctx, path); err == nil {
			t.Fatal("accepted invalid list path")
		}
	}
	valid := ChatAppendInput{ThreadID: chatID(1), ID: chatID(3), Role: "user", Source: "warden", Content: "valid"}
	for _, mutate := range []func(*ChatAppendInput){
		func(i *ChatAppendInput) { i.ID = "not-a-uuid" },
		func(i *ChatAppendInput) { i.ThreadID = "" },
		func(i *ChatAppendInput) { i.Role = "system" },
		func(i *ChatAppendInput) { i.Source = "unknown" },
		func(i *ChatAppendInput) { i.Content = "\n\t" },
		func(i *ChatAppendInput) { i.Content = "a\x00b" },
		func(i *ChatAppendInput) { i.Content = "\xff" },
		func(i *ChatAppendInput) { i.Content = strings.Repeat("x", MaxChatContentLen+1) },
		func(i *ChatAppendInput) { v := ""; i.ReplyTo = &v },
	} {
		in := valid
		mutate(&in)
		if _, err := s.AppendChatMessage(ctx, in); err == nil {
			t.Fatal("accepted invalid append")
		}
	}
	if _, err := s.CreateChatThread(ctx, "", "valid", ""); err == nil {
		t.Fatal("accepted missing UUID")
	}
	if _, err := s.ReadChatThread(ctx, "bad"); err == nil {
		t.Fatal("accepted invalid read UUID")
	}
	if _, err := s.RenameChatThread(ctx, "bad", "valid"); err == nil {
		t.Fatal("accepted invalid rename UUID")
	}
	if _, err := s.ReadChatThread(ctx, chatID(99)); !errors.Is(err, ErrChatThreadNotFound) {
		t.Fatal(err)
	}
	if _, err := s.RenameChatThread(ctx, chatID(99), "valid"); !errors.Is(err, ErrChatThreadNotFound) {
		t.Fatal(err)
	}
	missing := valid
	missing.ThreadID = chatID(99)
	if _, err := s.AppendChatMessage(ctx, missing); !errors.Is(err, ErrChatThreadNotFound) {
		t.Fatal(err)
	}
	// Inclusive boundaries are accepted, including multiline Markdown content.
	// The length cap is on the whole label, so the filler is sized against the
	// prefix this OS needs to make the path absolute.
	maxPath := fixturePath("/" + strings.Repeat("p", MaxChatPathLen-len(fixturePath("/"))-1))
	if len(maxPath) != MaxChatPathLen-1 {
		t.Fatalf("boundary path is %d bytes, want %d", len(maxPath), MaxChatPathLen-1)
	}
	if _, err := s.CreateChatThread(ctx, chatID(2), strings.Repeat("x", MaxChatTitleLen), maxPath); err != nil {
		t.Fatal(err)
	}
	valid.Content = strings.Repeat("x", MaxChatContentLen)
	if _, err := s.AppendChatMessage(ctx, valid); err != nil {
		t.Fatal(err)
	}
	calls := []func(context.Context) error{
		func(ctx context.Context) error { _, err := s.ListChatThreads(ctx, ""); return err },
		func(ctx context.Context) error { _, err := s.CreateChatThread(ctx, chatID(5), "valid", ""); return err },
		func(ctx context.Context) error { _, err := s.ReadChatThread(ctx, chatID(1)); return err },
		func(ctx context.Context) error { _, err := s.AppendChatMessage(ctx, valid); return err },
		func(ctx context.Context) error { _, err := s.RenameChatThread(ctx, chatID(1), "rename"); return err },
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, call := range calls {
		if err := call(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled call: %v", err)
		}
	}
	_ = s.db.Close()
	for _, call := range calls {
		if err := call(ctx); err == nil {
			t.Fatal("closed database returned success")
		}
	}
}
