// Command forge-runner installs and launches runners from a terminal.
//
// It exists for two reasons. One is that a module with a command-line face can
// be used by anything — a script, a living-room client, a service — and not only
// by a program that happens to link it. The other is more immediate: a great
// deal of what a host like PortForge does is hard to test because testing it
// needs a desktop session on particular hardware. Checking that an arm64 Mac
// build unpacks, or that a Windows path with a space in it survives, is a
// GUI session and a spare machine with this unavailable, and one ssh command
// with it.
//
//	forge-runner list
//	forge-runner install "Gopher64 · gopher64"
//	forge-runner launch  "Gopher64 · gopher64" /games/zelda.z64
//	forge-runner latest  "Gopher64 · gopher64"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zamiba/forge-runner"
	"github.com/zamiba/forge/engine"
)

func main() {
	root := flag.String("root", defaultRoot(), "directory the definitions and installs live under")
	platform := flag.String("platform", hostPlatform(), "target platform, as the specs spell it")
	quiet := flag.Bool("quiet", false, "do not print install progress")
	verbose := flag.Bool("v", false, "also print the full install log, as forge writes it")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}

	if err := run(flag.Arg(0), flag.Args()[1:], *root, *platform, *quiet, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "forge-runner:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `forge-runner installs and launches the programs that run media.

  forge-runner list
  forge-runner latest  <runner>
  forge-runner install <runner>
  forge-runner launch  <runner> <file>

<runner> is a runner's _itemTitle, as list prints it.

`)
	flag.PrintDefaults()
}

func run(cmd string, args []string, root, platform string, quiet, verbose bool) error {
	store, err := runner.Materialise(root)
	if err != nil {
		return err
	}

	switch cmd {
	case "list":
		return list(store, platform)
	case "latest", "install", "launch":
		if len(args) == 0 {
			return fmt.Errorf("%s needs a runner; try `forge-runner list`", cmd)
		}
		r, err := store.Find(args[0])
		if err != nil {
			return err
		}
		switch cmd {
		case "latest":
			tag, err := runner.LatestTag(r.Spec.ReleaseSource)
			if err != nil {
				return err
			}
			fmt.Println(tag)
			return nil
		case "install":
			return install(store, *r, root, platform, quiet, verbose)
		default:
			if len(args) < 2 {
				return fmt.Errorf("launch needs a file to run")
			}
			return launch(*r, store, root, platform, args[1])
		}
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func list(store runner.Store, platform string) error {
	runners, err := store.List()
	if err != nil {
		return err
	}
	if len(runners) == 0 {
		fmt.Println("no runners")
		return nil
	}
	for _, r := range runners {
		marks := []string{}
		if !store.Supports(r, platform) {
			marks = append(marks, "no build for "+platform)
		}
		if a := r.Item.Achievements; a != nil && !a.Hardcore {
			marks = append(marks, a.Provider+" softcore only")
		}
		note := ""
		if len(marks) > 0 {
			note = "  (" + strings.Join(marks, "; ") + ")"
		}
		fmt.Printf("%-28s runs %s%s\n", r.Item.ItemTitle, strings.Join(r.Spec.Runs, ", "), note)
	}
	return nil
}

func install(store runner.Store, r runner.Runner, root, platform string, quiet, verbose bool) error {
	dir := installDir(root, r)
	opts := runner.InstallOptions{Dir: dir, Platform: platform}
	// forge's log and the step events describe the same steps, so sending both
	// to stderr prints every line twice. The events are the readable summary;
	// the log is the whole output of whatever the steps invoked, which is wanted
	// only when something has gone wrong.
	if verbose {
		opts.Log = os.Stderr
	}
	if !quiet {
		// One line per step rather than a progress bar: the output is read in a
		// terminal or in a CI log, and a bar redraws into nonsense in both.
		opts.Events = func(e engine.Event) {
			if e.Kind == engine.EventStepStart {
				fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", e.Index+1, e.Total, e.Label)
			}
		}
	}

	done, err := store.Install(context.Background(), r, opts)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s installed for %s\n", r.Item.Title, done.Tag, done.Platform)
	for _, e := range done.Executables {
		fmt.Printf("  %s\n", filepath.Join(dir, e.Path))
	}
	return nil
}

func launch(r runner.Runner, store runner.Store, root, platform, file string) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	dir := installDir(root, r)

	// The executable comes from the spec rather than from a record, because this
	// command keeps no state. A host that does keeps its own and uses that; the
	// spec is the same answer either way, since it is what defined the
	// executable in the first place.
	exes, err := store.Executables(r, platform)
	if err != nil {
		return err
	}
	if len(exes) == 0 {
		return fmt.Errorf("%s declares no executable", r.Item.Title)
	}
	exe := exes[0]

	// Absolute, and this is not cosmetic: cmd.Dir changes the working directory
	// before the exec, and a relative cmd.Path is resolved *after* that — so
	// "install/gopher64" with Dir set to the same folder is looked for at
	// "install/install/gopher64" and fails with "no such file or directory"
	// about a file that is plainly there.
	exePath, err := filepath.Abs(filepath.Join(dir, exe.Path))
	if err != nil {
		return err
	}
	if _, err := os.Stat(exePath); err != nil {
		return fmt.Errorf("%s is not installed: %w", r.Item.Title, err)
	}

	args, err := runner.LaunchArgs(exe, runner.LaunchOptions{MediaPath: abs})
	if err != nil {
		return err
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = filepath.Dir(exePath)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func installDir(root string, r runner.Runner) string {
	return filepath.Join(root, "installs", r.Item.ItemType, r.Item.ItemTitle)
}

func defaultRoot() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "forge-runner")
	}
	return "forge-runner"
}

// hostPlatform spells this machine the way a spec's targetPlatforms does. It
// matches the host convention of the suite: an OS name, a hyphen, and an
// architecture, with the Go names mapped to the ones a release asset uses.
func hostPlatform() string {
	osName := "Linux"
	switch runtime.GOOS {
	case "windows":
		osName = "Windows"
	case "darwin":
		osName = "Mac"
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "x86"
	}
	return osName + "-" + arch
}
