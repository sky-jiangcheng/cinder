// app via `-ldflags "-X main.version=$(VERSION)"`. When built without
// ldflags the variable below is used as the default.
package version

// Version is the current Cinder version. The canonical value lives in
// wails.json (info.productVersion); scripts/bump-version.sh propagates it
// here. CI builds override this via ldflags at link time — this MUST stay a
// var: `-X` only rewrites string variables, a const silently ignores it.
var Version = "2.43.0"
