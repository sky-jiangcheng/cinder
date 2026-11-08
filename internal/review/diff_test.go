package review

import "testing"

const samplePatch = `diff --git a/src/main/java/a/Foo.java b/src/main/java/a/Foo.java
index 1111111..2222222 100644
--- a/src/main/java/a/Foo.java
+++ b/src/main/java/a/Foo.java
@@ -10,6 +10,8 @@ public class Foo {
     private int keep;
+    private String added1;
+    private String added2;
     void bar() {
         run();
     }
diff --git a/old/Name.java b/new/Name.java
similarity index 100%
rename from old/Name.java
rename to new/Name.java
diff --git a/img/logo.png b/img/logo.png
new file mode 100644
index 0000000..3333333
Binary files /dev/null and b/img/logo.png differ
diff --git a/gone/Dead.java b/gone/Dead.java
deleted file mode 100644
index 4444444..0000000
--- a/gone/Dead.java
+++ /dev/null
@@ -1,2 +0,0 @@
-class Dead {
-}
`

func TestParseUnifiedDiff(t *testing.T) {
	files := ParseUnifiedDiff(samplePatch)
	if len(files) != 4 {
		t.Fatalf("want 4 files, got %d: %+v", len(files), files)
	}

	foo := files[0]
	if foo.Path != "src/main/java/a/Foo.java" || foo.Status != StatusModified {
		t.Errorf("foo header: %+v", foo)
	}
	added := foo.Added()
	if len(added) != 2 || added[11] != "    private String added1;" || added[12] != "    private String added2;" {
		t.Errorf("added lines: %+v", added)
	}
	// Context lines must not be counted as additions: the hunk starts at new
	// line 10 (`private int keep;`) and line 13 is `void bar()`.
	if _, ok := added[10]; ok {
		t.Errorf("context line 10 reported as added")
	}
	if _, ok := added[13]; ok {
		t.Errorf("context line 13 reported as added")
	}
	if foo.ChangedLines() != 2 {
		t.Errorf("changed lines = %d, want 2", foo.ChangedLines())
	}

	ren := files[1]
	if ren.Status != StatusRenamed || ren.OldPath != "old/Name.java" || ren.Path != "new/Name.java" {
		t.Errorf("rename: %+v", ren)
	}
	if len(ren.Hunks) != 0 {
		t.Errorf("a pure rename should have no hunks, got %d", len(ren.Hunks))
	}

	if !files[2].Binary || files[2].Status != StatusAdded {
		t.Errorf("binary add: %+v", files[2])
	}

	del := files[3]
	if del.Status != StatusDeleted || len(del.Removed()) != 2 {
		t.Errorf("deletion: %+v", del)
	}
}

func TestParseUnifiedDiffEmpty(t *testing.T) {
	if got := ParseUnifiedDiff(""); len(got) != 0 {
		t.Errorf("empty patch produced %d files", len(got))
	}
}

func TestParseHunkHeaderDefaults(t *testing.T) {
	h := parseHunkHeader("@@ -5 +7 @@ foo()")
	if h == nil || h.OldStart != 5 || h.OldCount != 1 || h.NewStart != 7 || h.NewCount != 1 {
		t.Fatalf("defaults not applied: %+v", h)
	}
	if h.Header != "foo()" {
		t.Errorf("header = %q", h.Header)
	}
}
