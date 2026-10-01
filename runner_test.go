package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/zamiba/forge/engine"
)

func materialise(t *testing.T) Store {
	t.Helper()
	s, err := Materialise(t.TempDir())
	if err != nil {
		t.Fatalf("Materialise: %v", err)
	}
	return s
}

// Two ways the embed fails quietly, both worth a test because both report
// "contains no embeddable files" and neither names the file it could not take.
//
//  1. go:embed skips dot-prefixed names unless given "all:", and every file in a
//     MediaItem folder begins with one.
//  2. Embedded paths go through module.CheckFilePath, which allows Unicode
//     letters and digits but only a small set of ASCII punctuation — and U+00B7
//     MIDDLE DOT, the standard's own separator, is not in it. A folder named for
//     its _itemTitle cannot compile, which is why the tree is keyed by slug and
//     Materialise reads the title from the item.
func TestBuiltinRunnersAreComplete(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runners) == 0 {
		t.Fatal("no runners are embedded; if the tree is present but empty, the go:embed directive has lost its \"all:\" prefix")
	}

	for _, r := range runners {
		title := r.Item.ItemTitle
		if r.Item.Title == "" || r.Item.Description == "" {
			t.Errorf("%s: a runner needs a title and a description to be listable", title)
		}
		if r.Item.ParentItemType.Title == "" {
			t.Errorf("%s: no _parentItemType", title)
		}
		if len(r.Spec.Runs) == 0 {
			t.Errorf("%s: runs no item type, so nothing could ever be launched with it", title)
		}
		// _itemTitle composition is per item type, and for software it is
		// "Title · Publisher" — not Title · Year, which is the VideoGame rule.
		if want := r.Item.Title + " · " + r.Item.Publisher; title != want {
			t.Errorf("_itemTitle is %q, want %q (Title · Publisher)", title, want)
		}
		// For anything published from a Git forge the publisher is the account
		// owning the repository, so the two must agree or the title claims a
		// publisher the releases do not come from.
		if owner := r.Spec.ReleaseSource.Publisher(); owner != "" && owner != r.Item.Publisher {
			t.Errorf("%s: publisher is %q but releases come from %q", title, r.Item.Publisher, owner)
		}
		// Emulates is the program's fact and Runs is the host's dispatch, so a
		// runner may run less than it emulates but never more: dispatching a
		// system the program does not support is a promise nothing can keep.
		for _, it := range r.Spec.Runs {
			if len(r.Item.Emulates) > 0 && !contains(r.Item.Emulates, it) {
				t.Errorf("%s: runs %q but does not emulate it", title, it)
			}
		}
		if r.Spec.ReleaseSource.Host != "github" || r.Spec.ReleaseSource.Repo == "" {
			t.Errorf("%s: releaseSource is %+v; a built-in runner cannot hardcode a URL, so it must say where to look",
				title, r.Spec.ReleaseSource)
		}
		specs, err := s.Specs(r)
		if err != nil {
			t.Errorf("%s: %v", title, err)
			continue
		}
		if len(specs) == 0 {
			t.Errorf("%s: has no builds", title)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// A runner spec declares no versions, because it cannot know them — the release
// feed does. A spec that grows a hardcoded version is back to needing a release
// of the host for every emulator update.
func TestBuiltinSpecsResolveTheirTagAtInstallTime(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runners {
		specs, err := s.Specs(r)
		if err != nil {
			t.Fatal(err)
		}
		for i, spec := range specs {
			if len(spec.Versions) != 0 {
				t.Errorf("%s build %d declares versions %v; the tag is resolved at install time and passed as ${args.%s}",
					r.Item.ItemTitle, i, spec.Versions, TagArg)
			}
			for _, step := range spec.Steps {
				if step.Step == "fetch" && step.URL != "" && !strings.Contains(step.URL, "${args."+TagArg+"}") {
					t.Errorf("%s build %d fetches %q with no ${args.%s}, so it is pinned to one release forever",
						r.Item.ItemTitle, i, step.URL, TagArg)
				}
			}
			if stale := engine.UndeclaredArgs(&specs[i]); len(stale) > 0 {
				t.Errorf("%s build %d reads %v, which nothing declares", r.Item.ItemTitle, i, stale)
			}
			if stale := engine.UnbracedRefs(&specs[i]); len(stale) > 0 {
				t.Errorf("%s build %d writes %v without braces, so they are not substituted", r.Item.ItemTitle, i, stale)
			}
		}
	}
}

// Each platform's variables have to bind every ${platform.*} its build reads.
//
// engine.UndeclaredArgs cannot answer this: it accepts the union of every
// platform's bindings, which is right for a spec where one branch uses a
// variable another does not, and useless for an asset table where every row must
// be complete. A missing binding leaves the reference as literal text, so the
// fetch asks for a file called "${platform.asset}" — a 404 at install time on
// one architecture only, which is exactly the failure nobody has the hardware to
// notice.
func TestBuiltinPlatformVarsAreComplete(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	ref := regexp.MustCompile(`\$\{platform\.([^{}$]+)\}`)

	for _, r := range runners {
		specs, err := s.Specs(r)
		if err != nil {
			t.Fatal(err)
		}
		for i := range specs {
			spec := &specs[i]
			wanted := map[string]bool{}
			for _, step := range spec.Steps {
				fields := append([]string{step.URL, step.Src, step.Dest, step.Path, step.Cmd, step.Executable, step.If}, step.Args...)
				for _, f := range fields {
					for _, m := range ref.FindAllStringSubmatch(f, -1) {
						wanted[m[1]] = true
					}
				}
			}
			if len(wanted) == 0 {
				continue
			}
			if spec.PlatformVars == nil {
				t.Errorf("%s build %d reads %d ${platform.*} variables but declares targetPlatforms as a plain array, which binds none",
					r.Item.ItemTitle, i, len(wanted))
				continue
			}
			for _, p := range spec.TargetPlatforms {
				for name := range wanted {
					if spec.PlatformVars[p][name] == "" {
						t.Errorf("%s build %d reads ${platform.%s} but %s does not bind it", r.Item.ItemTitle, i, name, p)
					}
				}
			}
		}
	}
}

// Every platform a build names must be one a host can actually report, or the
// build is unreachable — an asset table entry nobody will select, which looks
// like support and is not. And no platform may be covered twice: Select takes
// the first and the other is dead.
func TestBuiltinPlatformsAreReachableAndUnique(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil {
		t.Fatal(err)
	}

	reachable := map[string]bool{}
	for _, os := range []string{"Linux", "Windows", "Mac"} {
		reachable[os] = true
		for _, arch := range []string{"x64", "x86", "arm64", "arm"} {
			reachable[os+"-"+arch] = true
		}
	}

	for _, r := range runners {
		specs, err := s.Specs(r)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for i := range specs {
			for _, p := range specs[i].TargetPlatforms {
				if !reachable[p] {
					t.Errorf("%s build %d targets %q, which no host reports", r.Item.ItemTitle, i, p)
				}
				if seen[p] {
					t.Errorf("%s: %q is covered by two builds; Select takes the first and the other is dead", r.Item.ItemTitle, p)
				}
				seen[p] = true
			}
		}
	}
}

// Supports has to follow the platform the host actually is, because it is what
// turns an Install button off. Gopher64 publishes no macOS x86_64 asset, so an
// Intel Mac is a real unsupported case rather than a hypothetical one.
func TestSupportsFollowsTheHostPlatform(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil || len(runners) == 0 {
		t.Fatal(err)
	}
	r := runners[0]

	for _, host := range []string{"Linux-x64", "Linux-arm64", "Windows-x64", "Windows-arm64", "Mac-arm64"} {
		if !s.Supports(r, host) {
			t.Errorf("%s should be supported: an asset is published for it", host)
		}
	}
	if s.Supports(r, "Mac-x64") {
		t.Error("Mac-x64 reported supported, but no macOS x86_64 asset is published")
	}
}

// Materialising renames: the embedded folder is an ASCII slug and the folder on
// disk is the _itemTitle, because go:embed cannot carry the separator. If that
// mapping silently became a straight copy, nothing would look wrong — a lookup
// would just go to a folder no item claims.
func TestMaterialiseRenamesSlugsToItemTitles(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil || len(runners) == 0 {
		t.Fatalf("List = %v, %v", runners, err)
	}
	for _, r := range runners {
		if !strings.Contains(r.Item.ItemTitle, " · ") {
			t.Errorf("%s: an _itemTitle with no \" · \" is the slug, not the title — the rename did not happen", r.Item.ItemTitle)
		}
		// All three files, because .runner.json is the one this package owns and
		// would be the easy one to leave behind in the rename.
		for _, f := range []string{MetadataFileName, SpecFileName, RunnerSpecFileName} {
			if _, err := os.Stat(filepath.Join(s.Dir, r.Item.ItemType, r.Item.ItemTitle, f)); err != nil {
				t.Errorf("%s: %v", r.Item.ItemTitle, err)
			}
		}
		// And Find locates it by title alone, which is all a host stores.
		if got, err := s.Find(r.Item.ItemTitle); err != nil || got.Item.ItemTitle != r.Item.ItemTitle {
			t.Errorf("Find(%q) = %v, %v", r.Item.ItemTitle, got, err)
		}
	}
}

// Idempotent and overwriting, because the definitions are the binary's and a
// hand-edited one is a file that will disagree with the code reading it. What it
// must not do is reach anything else in the tree — a host keeps its own state in
// the same folders.
func TestMaterialiseOverwritesDefinitionsAndLeavesTheRestAlone(t *testing.T) {
	root := t.TempDir()
	s, err := Materialise(root)
	if err != nil {
		t.Fatal(err)
	}
	runners, err := s.List()
	if err != nil || len(runners) == 0 {
		t.Fatal(err)
	}
	itemDir := filepath.Join(s.Dir, runners[0].Item.ItemType, runners[0].Item.ItemTitle)

	if err := os.WriteFile(filepath.Join(itemDir, MetadataFileName), []byte(`{"_itemTitle":"tampered"}`), 0644); err != nil {
		t.Fatal(err)
	}
	bystander := filepath.Join(itemDir, "notes.txt")
	if err := os.WriteFile(bystander, []byte("kept"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Materialise(root); err != nil {
		t.Fatalf("second Materialise: %v", err)
	}
	again, err := s.List()
	if err != nil {
		t.Fatalf("after re-materialising: %v", err)
	}
	if len(again) != len(runners) || again[0].Item.ItemTitle != runners[0].Item.ItemTitle {
		t.Errorf("the tampered definition was not replaced: got %v", again)
	}
	if b, err := os.ReadFile(bystander); err != nil || string(b) != "kept" {
		t.Errorf("a file we did not put there was disturbed: %q, %v", b, err)
	}
}

// LatestTag is what replaces a hardcoded URL, so it has to reject the shapes
// that would otherwise produce a nonsense request.
func TestLatestTagReadsTheFeedAndRefusesNonsense(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/name/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.3"})
		case "/repos/owner/untagged/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "untagged"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	old := APIBase
	APIBase = srv.URL
	defer func() { APIBase = old }()

	got, err := LatestTag(ReleaseSource{Host: "github", Repo: "owner/name"})
	if err != nil || got != "v1.2.3" {
		t.Fatalf("LatestTag = %q, %v; want v1.2.3", got, err)
	}

	for _, bad := range []ReleaseSource{
		{Host: "gitlab", Repo: "owner/name"},
		{Host: "github", Repo: ""},
		{Host: "github", Repo: "no-slash"},
		{Host: "github", Repo: "too/many/slashes"},
		{Host: "github", Repo: "owner/missing"},
		{Host: "github", Repo: "owner/untagged"},
	} {
		if _, err := LatestTag(bad); err == nil {
			t.Errorf("LatestTag(%+v) succeeded; want an error", bad)
		}
	}
}

// For is the dispatch question, so it has to answer both ways round and must not
// match an item type it merely contains as a substring.
func TestForMatchesOnlyDeclaredItemTypes(t *testing.T) {
	mk := func(title string, runs ...string) Runner {
		return Runner{
			Item: SoftwareEmulator{ItemTitle: title},
			Spec: RunnerSpec{Runs: runs},
		}
	}
	rs := []Runner{mk("A", "N64CartRom"), mk("B", "NESCartRom", "N64CartRom"), mk("C", "PS1DiscImage")}

	got := For(rs, "N64CartRom")
	if len(got) != 2 || got[0].Item.ItemTitle != "A" || got[1].Item.ItemTitle != "B" {
		t.Errorf("For(N64CartRom) = %v; want A and B in order", got)
	}
	if got := For(rs, "GBCartRom"); len(got) != 0 {
		t.Errorf("For(GBCartRom) = %v; want none", got)
	}
	if got := For(rs, "CartRom"); len(got) != 0 {
		t.Errorf("For matched on a substring: %v", got)
	}
}

// The media file reaches the program through the spec's own ${romPath}, so a
// spec that asks for something nothing provides must fail here rather than at
// the moment the program starts with a nonsense argument.
//
// forge's FixedProvider stats the path, which is worth knowing rather than
// working around: a file deleted or on an unmounted drive since the page was
// opened fails the launch with a message about the file, instead of starting an
// emulator that opens to a complaint of its own.
func TestLaunchArgsResolvesTheMediaPathAndRefusesWhatItCannot(t *testing.T) {
	exe := Executable{Path: "install/gopher64", Args: []string{"${romPath}"}}

	rom := filepath.Join(t.TempDir(), "zelda.z64")
	if err := os.WriteFile(rom, []byte("not really a rom"), 0644); err != nil {
		t.Fatal(err)
	}

	args, err := LaunchArgs(exe, LaunchOptions{MediaPath: rom})
	if err != nil {
		t.Fatalf("LaunchArgs: %v", err)
	}
	if len(args) != 1 || args[0] != rom {
		t.Errorf("args = %v, want the media path", args)
	}

	// Nothing mapped at all.
	if _, err := LaunchArgs(exe, LaunchOptions{}); err == nil {
		t.Error("a spec reading ${romPath} with no media path succeeded")
	}
	// Mapped, but gone since.
	if err := os.Remove(rom); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchArgs(exe, LaunchOptions{MediaPath: rom}); err == nil {
		t.Error("a media path that no longer exists was accepted")
	}

	// A spec with no references needs no providers and must not be made to
	// invent one — most programs take the file some other way.
	plain := Executable{Path: "install/thing", Args: []string{"--windowed"}}
	if got, err := LaunchArgs(plain, LaunchOptions{}); err != nil || len(got) != 1 || got[0] != "--windowed" {
		t.Errorf("LaunchArgs(no refs) = %v, %v", got, err)
	}
}

// Executables has to agree with what an install actually produces, because its
// whole purpose is answering "where is the program" for a caller that kept no
// record. If it disagreed, a launch would look for a file that is not there and
// blame the install.
func TestExecutablesMatchWhatTheSpecDefines(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil || len(runners) == 0 {
		t.Fatal(err)
	}
	r := runners[0]

	for _, host := range []string{"Linux-x64", "Windows-x64", "Mac-arm64"} {
		exes, err := s.Executables(r, host)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if len(exes) == 0 {
			t.Fatalf("%s: no executable declared", host)
		}
		for _, e := range exes {
			// An uninterpolated reference is the failure mode: Interpolate
			// deliberately leaves a name it has no value for, so a missing
			// platform variable shows up here as literal text rather than as a
			// blank path.
			if strings.Contains(e.Path, "${") {
				t.Errorf("%s: executable path %q still carries a reference", host, e.Path)
			}
			if e.Path == "" {
				t.Errorf("%s: empty executable path", host)
			}
			for _, a := range e.Args {
				if strings.Contains(a, "${platform.") || strings.Contains(a, "${version.") {
					t.Errorf("%s: argument %q still carries a reference", host, a)
				}
			}
		}
		// Windows and Linux ship differently named binaries, so the path must
		// actually follow the platform rather than coming back the same twice.
		if host == "Windows-x64" && !strings.HasSuffix(exes[0].Path, ".exe") {
			t.Errorf("Windows-x64 executable is %q, which is not a .exe", exes[0].Path)
		}
	}

	if _, err := s.Executables(r, "Mac-x64"); err == nil {
		t.Error("Mac-x64 returned executables, but no macOS x86_64 build exists")
	}
}

// The message for an unsupported machine has to name the machine, not the
// platform SelectPlatform fell back to. A program published only for Mac-arm64
// used to tell an Intel Mac "no build for Mac", which reads as there being no
// Mac build at all — and it listed nothing the person could act on.
func TestUnsupportedNamesTheHostAndWhatExists(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil || len(runners) == 0 {
		t.Fatal(err)
	}

	_, err = s.Executables(runners[0], "Mac-x64")
	if err == nil {
		t.Fatal("Mac-x64 was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Mac-x64") {
		t.Errorf("error is %q; it must name the machine, not the OS it fell back to", msg)
	}
	if !strings.Contains(msg, "Mac-arm64") {
		t.Errorf("error is %q; it must list what is published, which includes a Mac build", msg)
	}
}

// The declared dependencies are the pre-install warning, so what they must not
// be is an exhaustive dump of the binary's DT_NEEDED list: libc and libstdc++
// are on every glibc desktop, and declaring them invents failures on a musl
// system where there is no libc.so.6 to find and the program may still run.
func TestDeclaredDependenciesAreWorthWarningAbout(t *testing.T) {
	s := materialise(t)
	runners, err := s.List()
	if err != nil || len(runners) == 0 {
		t.Fatal(err)
	}

	// The ones every glibc desktop has. Declaring one is the mistake this
	// catches, because it would nag everybody about a library they have.
	universal := map[string]bool{
		"libc.so.6": true, "libm.so.6": true, "libstdc++.so.6": true,
		"libgcc_s.so.1": true, "libdl.so.2": true, "libpthread.so.0": true,
		"ld-linux-x86-64.so.2": true,
	}

	for _, r := range runners {
		for osName, dep := range r.Spec.Dependencies {
			if osName != "Linux" && osName != "Windows" && osName != "Mac" {
				t.Errorf("%s: dependencies keyed by %q; it is an OS name, not a platform", r.Item.ItemTitle, osName)
			}
			for _, lib := range dep.Libraries {
				if universal[lib] {
					t.Errorf("%s declares %s, which every glibc desktop has; declare what somebody might lack", r.Item.ItemTitle, lib)
				}
				// A soname, as the loader looks for it — not a package name.
				if !strings.Contains(lib, ".so") {
					t.Errorf("%s declares %q, which is not a soname", r.Item.ItemTitle, lib)
				}
			}
			// A soname is not installable, so a declaration without a hint tells
			// somebody they are missing something and not how to get it.
			if len(dep.Libraries) > 0 && dep.Hint == "" {
				t.Errorf("%s declares libraries for %s with no hint; a soname is not a package name", r.Item.ItemTitle, osName)
			}
		}
	}
}

// For accepts an OS name or a full platform name, because callers have one or
// the other depending on how far through an install they are.
func TestDependenciesForAcceptsEitherName(t *testing.T) {
	d := Dependencies{"Linux": {Libraries: []string{"libfoo.so.1"}, Hint: "foo"}}
	for _, name := range []string{"Linux", "Linux-x64", "Linux-arm64"} {
		if got := d.For(name); len(got.Libraries) != 1 {
			t.Errorf("For(%q) = %+v, want the Linux entry", name, got)
		}
	}
	if got := d.For("Mac-arm64"); len(got.Libraries) != 0 {
		t.Errorf("For(Mac-arm64) = %+v, want nothing", got)
	}
}

// MissingLibraries is a name search, so it must find what is plainly there and
// must not claim something absent is present.
func TestMissingLibrariesFindsWhatIsThere(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("library searching only applies on Linux")
	}
	// libc is present on any machine running this test.
	if got := MissingLibraries([]string{"libc.so.6"}); len(got) != 0 {
		t.Errorf("MissingLibraries(libc.so.6) = %v; it is on this machine", got)
	}
	if got := MissingLibraries([]string{"libdefinitelynotreal.so.99"}); len(got) != 1 {
		t.Errorf("MissingLibraries(nonsense) = %v, want one entry", got)
	}
	// Order is the declared order, so a message reads the way it was written.
	got := MissingLibraries([]string{"libnope1.so.1", "libc.so.6", "libnope2.so.2"})
	if len(got) != 2 || got[0] != "libnope1.so.1" || got[1] != "libnope2.so.2" {
		t.Errorf("MissingLibraries = %v, want the two absent ones in order", got)
	}
	if got := MissingLibraries(nil); got != nil {
		t.Errorf("MissingLibraries(nil) = %v", got)
	}
}

// VerifyLinked must report nothing for a program that can start, because a false
// complaint would block a launch that would have worked.
func TestVerifyLinkedPassesSomethingThatRuns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ldd only applies on Linux")
	}
	if _, err := exec.LookPath("ldd"); err != nil {
		t.Skip("no ldd on this machine")
	}
	// The test binary itself links successfully, by construction: it is running.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := VerifyLinked(self); len(got) != 0 {
		t.Errorf("VerifyLinked(self) = %v; this binary is running, so it links", got)
	}
}

// And nothing for a path that is not a program, rather than a confusing
// complaint: a check that cannot run must not invent a failure.
func TestVerifyLinkedInventsNothingForANonProgram(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ldd only applies on Linux")
	}
	f := filepath.Join(t.TempDir(), "notabinary")
	if err := os.WriteFile(f, []byte("hello"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := VerifyLinked(f); len(got) != 0 {
		t.Errorf("VerifyLinked(a text file) = %v, want nothing", got)
	}
}
