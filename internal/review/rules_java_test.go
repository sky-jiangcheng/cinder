package review

import (
	"sort"
	"strings"
	"testing"
)

// newFile builds a File the way the service does: content as committed, plus
// the set of line numbers the commit added. addedLines are 1-based.
func newFile(path, content string, added ...int) *File {
	f := &File{Path: path, Content: content, Status: StatusModified, Added: map[int]string{}}
	lines := f.Lines()
	for _, n := range added {
		if n >= 1 && n <= len(lines) {
			f.Added[n] = lines[n-1]
		}
	}
	return f
}

type fakeRepo map[string]string

func (r fakeRepo) GrepJava(literal string) []string {
	var hits []string
	for path, content := range r {
		if strings.Contains(content, literal) {
			hits = append(hits, path)
		}
	}
	sort.Strings(hits)
	return hits
}

func (r fakeRepo) Read(path string) (string, bool) {
	c, ok := r[path]
	return c, ok
}

// run executes one rule and returns its findings.
func run(t *testing.T, rule Rule, f *File, repo Repo) []Finding {
	t.Helper()
	return rule.Check(f, repo)
}

func wantCount(t *testing.T, got []Finding, want int) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("want %d findings, got %d: %v", want, len(got), got)
	}
}

const feignClientSrc = `package a;

@FeignClient(name = "pay")
public interface PayClient {

    @PostMapping("/pay/query")
    Result<PayInfo> query(PayQueryReq req);

    @PostMapping("/pay/ok")
    Result<PayInfo> ok(@RequestBody PayQueryReq req);

    @PostMapping("/pay/two")
    Result<PayInfo> two(PayQueryReq req, String channel);

    @GetMapping("/pay/get")
    Result<PayInfo> get(@RequestBody PayQueryReq req);
}
`

func TestFeignMissingBody(t *testing.T) {
	// lines: 7 = query(...), 10 = ok(...), 13 = two(...), 16 = get(...)
	f := newFile("src/main/java/a/PayClient.java", feignClientSrc, 7, 10, 13, 16)
	got := run(t, feignMissingBody{}, f, nil)
	wantCount(t, got, 2)

	byLine := map[int]Finding{}
	for _, x := range got {
		byLine[x.Line] = x
	}
	if x, ok := byLine[7]; !ok || x.Severity != SeverityMedium {
		t.Errorf("single complex param should be medium, got %+v", x)
	}
	if x, ok := byLine[13]; !ok || x.Severity != SeverityHigh {
		t.Errorf("two params should be high, got %+v", x)
	}
	for _, x := range got {
		if x.Verdict != VerdictUnverified {
			t.Errorf("feign_missing_body must stay unverified: %+v", x)
		}
	}
}

func TestFeignMissingBodyIgnoresUntouchedMethods(t *testing.T) {
	// The commit only touched the @GetMapping line, so the older offending
	// methods must not be reported — that is the whole point of incremental.
	f := newFile("src/main/java/a/PayClient.java", feignClientSrc, 16)
	if got := run(t, feignMissingBody{}, f, nil); len(got) != 0 {
		t.Fatalf("reported pre-existing code: %v", got)
	}
}

func TestFeignMissingBodyNotAFeignInterface(t *testing.T) {
	src := `package a;
public interface Other {
    @PostMapping("/x")
    Result<Foo> x(FooReq req);
}
`
	f := newFile("src/main/java/a/Other.java", src, 4)
	if got := run(t, feignMissingBody{}, f, nil); len(got) != 0 {
		t.Fatalf("non-Feign interface reported: %v", got)
	}
}

func TestFeignGetBody(t *testing.T) {
	f := newFile("src/main/java/a/PayClient.java", feignClientSrc, 16)
	got := run(t, feignGetBody{}, f, nil)
	wantCount(t, got, 1)
	if got[0].Severity != SeverityHigh || got[0].Verdict != VerdictConfirmed {
		t.Errorf("want high/confirmed, got %+v", got[0])
	}
}

func TestRouteRenamed(t *testing.T) {
	src := `package a;
@RestController
public class PayController {
    @PostMapping("/pay/query")
    Result<Foo> query() { return null; }
}
`
	f := newFile("src/main/java/a/PayController.java", src, 4)
	f.Removed = []string{`    @PostMapping("/pay/queryV1")`}

	repo := fakeRepo{
		"src/main/java/b/Consumer.java": `@FeignClient("pay") interface C { @PostMapping("/pay/queryV1") void call(); }`,
		"src/main/java/a/PayController.java": src,
	}
	got := run(t, routeRenamed{}, f, repo)
	wantCount(t, got, 1)
	if got[0].Severity != SeverityHigh || got[0].Verdict != VerdictConfirmed {
		t.Errorf("want high/confirmed, got %+v", got[0])
	}
	if !strings.Contains(got[0].Message, "Consumer.java") {
		t.Errorf("message should name the file still referencing the old path: %s", got[0].Message)
	}
}

func TestRouteRenamedNoOtherCallers(t *testing.T) {
	src := `package a;
@RestController
public class PayController {
    @PostMapping("/pay/query")
    Result<Foo> query() { return null; }
}
`
	f := newFile("src/main/java/a/PayController.java", src, 4)
	f.Removed = []string{`    @PostMapping("/pay/queryV1")`}
	repo := fakeRepo{"src/main/java/a/PayController.java": src}
	if got := run(t, routeRenamed{}, f, repo); len(got) != 0 {
		t.Fatalf("no other caller, should be silent: %v", got)
	}
}

func TestFeignArity(t *testing.T) {
	src := `package a;
@FeignClient("pay")
public interface PayClient {
    Result<Foo> query(FooReq req, String channel);
}
`
	f := newFile("src/main/java/a/PayClient.java", src, 4)
	f.Removed = []string{`    Result<Foo> query(FooReq req);`}
	got := run(t, feignArity{}, f, nil)
	wantCount(t, got, 1)
	if got[0].Verdict != VerdictUnverified {
		t.Errorf("provider is not local, verdict must be unverified: %+v", got[0])
	}
}

func TestSwallowException(t *testing.T) {
	src := `package a;
public class Svc {
    void empty() {
        try { run(); } catch (Exception e) { }
    }
    void printOnly() {
        try { run(); } catch (Exception e) {
            e.printStackTrace();
        }
    }
    void logOnly() {
        try { run(); } catch (Exception e) {
            log.error("failed", e);
        }
    }
    void rethrows() {
        try { run(); } catch (Exception e) {
            log.error("failed", e);
            throw new IllegalStateException(e);
        }
    }
}
`
	f := newFile("src/main/java/a/Svc.java", src, 4, 7, 12, 17)
	got := run(t, swallowException{}, f, nil)
	wantCount(t, got, 3)
	for _, x := range got {
		if x.Line == 17 {
			t.Errorf("rethrow must not be reported: %+v", x)
		}
	}
}

func TestTxSelfCall(t *testing.T) {
	src := `package a;
public class Svc {
    @Transactional
    public void outer() {
        this.inner();
    }
    @Transactional
    public void inner() { }
    public void other() {
        this.helper();
    }
    void helper() { }
}
`
	f := newFile("src/main/java/a/Svc.java", src, 5, 10)
	got := run(t, txSelfCall{}, f, nil)
	wantCount(t, got, 1)
	if got[0].Line != 5 || got[0].Severity != SeverityHigh {
		t.Errorf("want line 5 high, got %+v", got[0])
	}
}

func TestSQLInjection(t *testing.T) {
	src := `package a;
public class Dao {
    String a(String id) { return "select * from t where id = '" + id + "'"; }
    String b(String id) { return "select * from t where id = ?"; }
    String c(String order) { return "select * from t order by ${order}"; }
}
`
	f := newFile("src/main/java/a/Dao.java", src, 3, 4, 5)
	got := run(t, sqlInjection{}, f, nil)
	wantCount(t, got, 2)
	if got[0].Line != 3 || got[0].Severity != SeverityHigh || got[0].Verdict != VerdictConfirmed {
		t.Errorf("concatenation should be high/confirmed: %+v", got[0])
	}
	if got[1].Line != 5 || got[1].Severity != SeverityMedium {
		t.Errorf("${} should be medium: %+v", got[1])
	}
}

func TestSensitiveLog(t *testing.T) {
	src := `package a;
public class Svc {
    void a(String phone) { log.info("phone={}", phone); }
    void b(String x) { log.info("x={}", x); }
}
`
	f := newFile("src/main/java/a/Svc.java", src, 3, 4)
	got := run(t, sensitiveLog{}, f, nil)
	wantCount(t, got, 1)
	if !(sensitiveLog{}).OptIn() {
		t.Error("sensitive_log must stay opt-in")
	}
}

func TestSharedDateFormat(t *testing.T) {
	src := `package a;
public class Svc {
    private static final SimpleDateFormat FMT = new SimpleDateFormat("yyyy-MM-dd");
    private final SimpleDateFormat local = new SimpleDateFormat("yyyy-MM-dd");
}
`
	f := newFile("src/main/java/a/Svc.java", src, 3, 4)
	got := run(t, sharedDateFormat{}, f, nil)
	wantCount(t, got, 1)
	if got[0].Line != 3 {
		t.Errorf("want the static one, got %+v", got[0])
	}
}

func TestRemoteInLoop(t *testing.T) {
	src := `package a;
public class Svc {
    void a(List<Item> items) {
        for (Item it : items) {
            stockClient.query(it.getId());
        }
    }
    void b(List<Item> items) {
        for (Item it : items) {
            helper(it);
        }
    }
}
`
	f := newFile("src/main/java/a/Svc.java", src, 5, 10)
	got := run(t, remoteInLoop{}, f, nil)
	wantCount(t, got, 1)
	if got[0].Line != 5 {
		t.Errorf("want the remote call, got %+v", got[0])
	}
}

func TestRulesSkipGeneratedAndNonJava(t *testing.T) {
	for _, path := range []string{
		"src/main/java/a/Svc.java", // sanity: this one IS java
	} {
		if !isJava(&File{Path: path}) {
			t.Errorf("%s should be reviewed", path)
		}
	}
	for _, path := range []string{
		"target/generated-sources/a/Svc.java",
		"web/src/api/x.ts",
		"docs/index.md",
	} {
		if isJava(&File{Path: path}) {
			t.Errorf("%s should be skipped", path)
		}
	}
}

func TestDefaultRulesHaveIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range DefaultRules() {
		if r.ID() == "" || seen[r.ID()] {
			t.Errorf("rule id empty or duplicated: %q", r.ID())
		}
		seen[r.ID()] = true
	}
	if len(seen) != 10 {
		t.Errorf("ADR-0053 D3 lists 10 rules, DefaultRules has %d", len(seen))
	}
}
