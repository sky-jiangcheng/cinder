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
		"src/main/java/b/Consumer.java":      `@FeignClient("pay") interface C { @PostMapping("/pay/queryV1") void call(); }`,
		"src/main/java/a/PayController.java": src,
	}
	got := run(t, routeRenamed{}, f, repo)
	wantCount(t, got, 1)
	if got[0].Severity != SeverityHigh || got[0].Verdict != VerdictUnverified {
		t.Errorf("want high/unverified (D3 lists a false-positive boundary), got %+v", got[0])
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
    // multiLineLogAlarm: THE shape. "} catch (Exception e) {" on its own
    // line with the try's "}" closing the previous one — real production
    // formatting. matchBrace used to treat the header's "} {" pair as a
    // net-zero block, returned the header as its own end, and every catch in
    // every reviewed file came out "empty" (2026-11-09 labelling: 17 of 20
    // candidates). The body logs AND alarms, which is handled: no finding.
    void multiLineLogAlarm() {
        try {
            run();
        } catch (Exception e) {
            log.error("failed", e);
            PlatformClientForAlarm.sendAlarmMessage("failed", e);
        }
    }
    // A multi-line catch that only logs is the real finding.
    void multiLineLogOnly() {
        try {
            run();
        } catch (Exception e) {
            log.error("failed", e);
        }
    }
    // A multi-line catch that returns failure to the caller is handled: the
    // caller does learn about it.
    void multiLineReturnsFailure() {
        try {
            return EasyMsResponseEntity.ofSuccess();
        } catch (Exception e) {
            log.warn("precheck failed", e);
            return EasyMsResponseEntity.ofFail(e.getMessage());
        }
    }
    // A multi-line catch that rethrows is handled.
    void multiLineRethrow() {
        try {
            run();
        } catch (Exception e) {
            log.error("bad draft", e);
            throw new BusinessException("Draft data format failed!");
        }
    }
}
`
	f := newFile("src/main/java/a/Svc.java", src, 4, 7, 12, 17, 31, 40, 49, 58)
	got := run(t, swallowException{}, f, nil)
	// empty(4), printOnly(7), logOnly(12), multiLineLogOnly(40).
	// NOT: rethrows(17), multiLineLogAlarm(31), multiLineReturnsFailure(49),
	// multiLineRethrow(58).
	wantCount(t, got, 4)
	for _, x := range got {
		if x.Line == 17 || x.Line == 31 || x.Line == 49 || x.Line == 58 {
			t.Errorf("handled shape must not be reported: %+v", x)
		}
	}
	// The multi-line finding must NOT say "empty" — the whole point of the
	// labelling run is that the message was a lie 17 times.
	for _, x := range got {
		if x.Line == 40 {
			if strings.Contains(x.Message, "空") {
				t.Errorf("multi-line log-only catch mislabelled as empty: %+v", x)
			}
		}
	}
}

// TestSwallowExceptionInTestSource pins the shape the 2026-11-09 labelling
// run tripped three times: JUnit asserting an exception, which reads exactly
// like a swallow to a brace-matching rule.
func TestSwallowExceptionInTestSource(t *testing.T) {
	src := `package a;
public class SvcTest {
    void assertsThrow() {
        try {
            service.check();
            fail("A KYC decision is still pending");
        } catch (BusinessException expected) {
            assertTrue(expected.getMessage());
        }
    }
}
`
	// The path is the discriminator, not the shape: this exact code in
	// src/main/ MUST still be reported by the rules above's logic.
	f := newFile("src/test/java/a/SvcTest.java", src, 4, 8)
	if got := run(t, swallowException{}, f, nil); len(got) != 0 {
		t.Errorf("test sources are out of scope: %+v", got)
	}
	byName := newFile("src/main/java/a/SvcTest.java", src, 4, 8)
	if got := run(t, swallowException{}, byName, nil); len(got) != 0 {
		t.Errorf("*Test.java is out of scope whatever the directory: %+v", got)
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
    String d(String name) { return "The identification document of " + name + ", please update the documents."; }
}
`
	f := newFile("src/main/java/a/Dao.java", src, 3, 4, 5, 6)
	got := run(t, sqlInjection{}, f, nil)
	wantCount(t, got, 2)
	if got[0].Line != 3 || got[0].Severity != SeverityHigh {
		t.Errorf("concatenation should be high: %+v", got[0])
	}
	if got[0].Verdict != VerdictUnverified {
		t.Errorf("sql_injection has a documented false-positive boundary, so it must not claim confirmed: %+v", got[0])
	}
	if got[1].Line != 5 || got[1].Severity != SeverityMedium {
		t.Errorf("${} should be medium: %+v", got[1])
	}
	// Line 6 is the labelling run's real false positive: English notification
	// copy where "update" is prose and the '+' is message assembly. The SQL
	// keyword must live inside a literal that STARTS like SQL.
}

// TestSQLInjectionLiteralPlusLiteral pins the other half of the labelling
// run's sql_injection false positives: `@Select("select ... " + "where ...")`
// across two lines. Concatenating two string CONSTANTS is folded by javac
// into one literal — nothing is injectable, and the shape is how MyBatis
// annotations stay readable past the line-length limit. Both candidates in
// the 40-day run were this.
func TestSQLInjectionLiteralPlusLiteral(t *testing.T) {
	src := `package a;
public interface Dao {
    @Select("select r.user_id from user_cst_relation r " +
            "where not exists (select 1 from user_info u where u.id = r.user_id)")
    List<Long> queryRelationWithoutUser();
    @Select("select u.id from user_info u " +
            "where not exists (select 1 from user_cst_relation r where r.user_id = u.id)")
    List<Long> queryUserWithoutRelation();
    String risky(String id) {
        return "select * from t where id = '" + id + "'";
    }
}
`
	f := newFile("src/main/java/a/Dao.java", src, 3, 4, 6, 7, 10)
	got := run(t, sqlInjection{}, f, nil)
	wantCount(t, got, 1)
	if got[0].Line != 10 {
		t.Errorf("want the variable splice at 10, got %+v", got)
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

// TestRemoteInLoopBatchPagination pins the labelling run's other false
// positive: batch paging that sends the WHOLE list per round.
func TestRemoteInLoopBatchPagination(t *testing.T) {
	src := `package a;
public class Svc {
    int total;
    void fix(List<String> identiferNoList, int batchSize) {
        while (true) {
            FixSameNameRemitFlagRsp rsp = piFeign.fixSameNameRemitFlag(FixSameNameRemitFlagReq.builder()
                    .identiferNoList(identiferNoList)
                    .batchSize(batchSize)
                    .build());
            total += rsp.getUpdateCount();
            if (rsp.getUpdateCount() < batchSize) {
                break;
            }
        }
    }
    void stillFires(List<Item> items) {
        for (Item it : items) {
            piFeign.fixOne(it.getId());
        }
    }
}
`
	// The while-true call (lines 6-10) is batching: no iteration variable, so
	// no N+1. The for-each call (line 18) touches `it` and must fire.
	f := newFile("src/main/java/a/Svc.java", src, 6, 7, 8, 9, 10, 18)
	got := run(t, remoteInLoop{}, f, nil)
	wantCount(t, got, 1)
	if got[0].Line != 18 {
		t.Errorf("want the per-item call at 18, got %+v", got)
	}
}

func TestRouteRenamedCosmeticSlash(t *testing.T) {
	// The exact commit the 2026-11-09 labelling run flagged: `"checkModify"`
	// → `"/checkModify"` across a whole controller. Spring treats the
	// leading slash as optional, so no route moved — and every consumer had
	// already been updated, so the old text only survives as a substring of
	// the new one. The first version reported 30 high findings from this.
	src := `package a;
@RestController
public class CstController {
    @PostMapping("/checkModify")
    public Entity checkModify(@RequestBody Req req) { return null; }
}
`
	f := newFile("src/main/java/a/CstController.java", src, 4)
	f.Removed = []string{`    @PostMapping("checkModify")`}

	repo := fakeRepo{
		// Updated consumer: declares the same route with the new spelling.
		"src/main/java/b/CstClient.java": `@FeignClient("cst") interface C { @PostMapping("/checkModify") void call(); }`,
	}
	if got := run(t, routeRenamed{}, f, repo); len(got) != 0 {
		t.Errorf("a leading slash is not a rename: %+v", got)
	}
}

func TestRouteRenamedUpdatedConsumerNotCounted(t *testing.T) {
	// A genuine rename, and the consumer was updated in the same sweep.
	// GrepJava(old) still hits because "queryV1" is a substring of
	// "/queryV2"? No — it hits because the old literal remains somewhere.
	// Either way, comparing ROUTES is what keeps this quiet.
	src := `package a;
@RestController
public class PayController {
    @PostMapping("/pay/queryV2")
    Result<Foo> query() { return null; }
}
`
	f := newFile("src/main/java/a/PayController.java", src, 4)
	f.Removed = []string{`    @PostMapping("/pay/queryV1")`}
	repo := fakeRepo{
		"src/main/java/b/Consumer.java": `@FeignClient("pay") interface C { @PostMapping("/pay/queryV2") void call(); }`,
	}
	if got := run(t, routeRenamed{}, f, repo); len(got) != 0 {
		t.Errorf("consumer already migrated, nothing to report: %+v", got)
	}
}

func TestCanonicalPath(t *testing.T) {
	for _, pair := range [][2]string{
		{"/x", "x"}, {"x", "x"}, {"/x/", "x"}, {"  /x  ", "x"}, {"", ""}, {"/", ""},
		{"/pay/query", "pay/query"},
	} {
		if got := canonicalPath(pair[0]); got != pair[1] {
			t.Errorf("canonicalPath(%q) = %q, want %q", pair[0], got, pair[1])
		}
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

// ADR-0053 D1: a Feign client in repo A has no binder, and the provider in
// repo B (not part of this commit) binds the same route with @RequestBody.
func TestFeignMissingBodyCrossRepoProvider(t *testing.T) {
	src := `package a;

@FeignClient(name = "pay")
public interface PayClient {

    @PostMapping("/pay/query")
    Result<PayInfo> query(PayQueryReq req);
}
`
	f := newFile("src/main/java/a/PayClient.java", src, 7)
	provider := `package b;

@RestController
public class PayController {

    @PostMapping("/pay/query")
    Result<PayInfo> query(@RequestBody PayQueryReq req) { return null; }
}
`
	repo := fakeRepo{
		PathKey("/repos/service-b", "src/main/java/b/PayController.java"): provider,
	}
	got := run(t, feignMissingBody{}, f, repo)
	wantCount(t, got, 1)
	if !strings.Contains(got[0].Evidence, "/repos/service-b") || !strings.Contains(got[0].Evidence, "PayController.java") {
		t.Errorf("evidence should name the provider repository and file: %q", got[0].Evidence)
	}
	if !strings.Contains(got[0].Evidence, "@RequestBody") {
		t.Errorf("evidence should carry the provider's @RequestBody: %q", got[0].Evidence)
	}
	if got[0].Verdict != VerdictUnverified {
		t.Errorf("verdict must stay unverified: %+v", got[0])
	}
}

// A provider that also omits @RequestBody matches the contract, so the
// cross-repo lookup must not add evidence or a second finding.
func TestFeignMissingBodyCrossRepoMatchingContract(t *testing.T) {
	src := `package a;

@FeignClient(name = "pay")
public interface PayClient {

    @PostMapping("/pay/query")
    Result<PayInfo> query(PayQueryReq req);
}
`
	f := newFile("src/main/java/a/PayClient.java", src, 7)
	provider := `package b;
@RestController
public class PayController {
    @PostMapping("/pay/query")
    Result<PayInfo> query(PayQueryReq req) { return null; }
}
`
	repo := fakeRepo{
		PathKey("/repos/service-b", "src/main/java/b/PayController.java"): provider,
	}
	got := run(t, feignMissingBody{}, f, repo)
	wantCount(t, got, 1)
	if strings.Contains(got[0].Evidence, "service-b") {
		t.Errorf("a matching provider must not add cross-repo evidence: %q", got[0].Evidence)
	}
}

const arityClientSrc = `package a;

@FeignClient("pay")
public interface PayClient {

    @PostMapping("/pay/query")
    Result<Foo> query(FooReq req, String channel);
}
`

const arityProviderSrc = `package b;
@RestController
public class PayController {
    @PostMapping("/pay/query")
    Result<Foo> query(@RequestBody FooReq req) { return null; }
}
`

// feign_arity compares parameter counts against the cross-repo provider.
func TestFeignArityCrossRepoMismatch(t *testing.T) {
	f := newFile("src/main/java/a/PayClient.java", arityClientSrc, 7)
	f.Removed = []string{`    Result<Foo> query(FooReq req);`}
	repo := fakeRepo{
		PathKey("/repos/service-b", "src/main/java/b/PayController.java"): arityProviderSrc,
	}
	got := run(t, feignArity{}, f, repo)
	wantCount(t, got, 1)
	if !strings.Contains(got[0].Evidence, "PayController.java") {
		t.Errorf("evidence should carry the provider signature: %q", got[0].Evidence)
	}
	if !strings.Contains(got[0].Message, "service-b") || !strings.Contains(got[0].Message, "参数个数不匹配") {
		t.Errorf("message should name the provider repo and the mismatch: %q", got[0].Message)
	}
}

// Without a provider (nil or empty index) arity stays unverified, no panic.
func TestFeignArityProviderAbsentStaysUnverified(t *testing.T) {
	f := newFile("src/main/java/a/PayClient.java", arityClientSrc, 7)
	f.Removed = []string{`    Result<Foo> query(FooReq req);`}
	if got := run(t, feignArity{}, f, nil); len(got) != 1 || got[0].Verdict != VerdictUnverified {
		t.Fatalf("nil repo must keep the unverified finding: %+v", got)
	}
	got := run(t, feignArity{}, f, fakeRepo{})
	if len(got) != 1 || got[0].Verdict != VerdictUnverified {
		t.Fatalf("empty index must keep the unverified finding: %+v", got)
	}
	if !strings.Contains(got[0].Suggestion, "人工确认") {
		t.Errorf("the manual-check wording must survive: %q", got[0].Suggestion)
	}
}

// Encoded keys must be excluded via f.Key and rendered without NUL bytes.
func TestRouteRenamedEncodedKeyExcludesSelf(t *testing.T) {
	src := `package a;
@RestController
public class PayController {
    @PostMapping("/pay/queryV2")
    Result<Foo> query() { return null; }
}
`
	const rootA = "/repos/service-a"
	const rootB = "/repos/service-b"
	f := newFile("src/main/java/a/PayController.java", src, 4)
	f.Key = PathKey(rootA, "src/main/java/a/PayController.java")
	f.Removed = []string{`    @PostMapping("/pay/queryV1")`}
	repo := fakeRepo{
		// A still mentions the old route; its encoded key equals f.Key.
		PathKey(rootA, "src/main/java/a/PayController.java"): `@PostMapping("/pay/queryV1") void legacy();`,
		PathKey(rootB, "src/main/java/b/Consumer.java"):      `@FeignClient("pay") interface C { @PostMapping("/pay/queryV1") void call(); }`,
	}
	got := run(t, routeRenamed{}, f, repo)
	wantCount(t, got, 1)
	if !strings.Contains(got[0].Message, rootB) {
		t.Errorf("message should name the other repository: %q", got[0].Message)
	}
	if strings.Contains(got[0].Message, rootA) {
		t.Errorf("the changed repository must not be listed as its own caller: %q", got[0].Message)
	}
	if strings.Contains(got[0].Message, "\x00") {
		t.Errorf("message must not contain NUL bytes: %q", got[0].Message)
	}
}
