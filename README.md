# forge-runner

Installs and launches the programs that run media a host cannot run itself:
emulators, and compatibility layers such as Proton.

"Runner" rather than "emulator" because Proton is neither an emulator nor a
special case, and Lutris has used the word for exactly this for years.

## What is here, and what is not

This module owns a runner's definition, finding its newest release, installing it
through [forge](https://github.com/zamiba/forge), and building the command line
to launch something with it.

It owns **none** of what follows from a game being up: no one-at-a-time slot, no
profiles, no playtime, no events beyond the ones forge reports while installing.
Those belong to the host. PortForge and a living-room client will answer them
differently, and a module that decided for them would be useless to the second
one.

That division is where the reuse lives: two hosts can disagree about what owning
a running game means while agreeing exactly on which asset to download for an
arm64 Mac.

## A runner is three files

```
<ItemType>/<_itemTitle>/
  .mediaitem.json   the MediaItem — what the program is       (go-mediaitems' format)
  .forge.json       how to build or unpack it                 (forge's format)
  .runner.json      what to install, and what to send it      (this module's)
```

Being a runner is not a kind of program, it is a job given to one — which is why
`.runner.json` exists rather than those fields living in the MediaItem. It keeps
every forge-shaped concern out of a schema a MediaItem database will also carry,
and it means a compatibility layer needs no new item type: it is a
`SoftwareApplication` with a `.runner.json` beside it.

## `fetch` takes a literal URL, so the tag is resolved at install time

forge has no notion of "latest release", deliberately. A definition shipped
inside a binary therefore cannot hardcode a URL: that would make an emulator
update need a release of the host, and a dead link could not be fixed without
one.

So `.runner.json` says **where to look**, the spec writes `${args.tag}` into its
fetch URL, and `Install` resolves the newest tag from the release feed and passes
it. The spec declares no versions at all, which is the truthful account — it does
not know them, the feed does.

## Usage

```go
store, err := runner.Materialise(configDir)       // definitions to disk, once
runners, err := store.List()
done, err := store.Install(ctx, r, runner.InstallOptions{
    Dir: installDir, Platform: "Linux-x64", Events: onEvent, Log: logFile,
})
args, err := runner.LaunchArgs(exe, runner.LaunchOptions{MediaPath: romPath})
```

A command-line face comes with it, which is the cheapest way to check a real
install on hardware a test cannot reach:

```
forge-runner list
forge-runner latest  "Gopher64 · gopher64"
forge-runner install "Gopher64 · gopher64"
forge-runner launch  "Gopher64 · gopher64" /games/zelda.z64
```

## System dependencies: two checks, because one cannot do it

`.runner.json` declares what must already be on the machine:

```json
"dependencies": {
  "Linux": {
    "libraries": ["libfontconfig.so.1"],
    "hint": "fontconfig — Debian/Ubuntu: libfontconfig1 · Arch: fontconfig · Fedora: fontconfig"
  }
}
```

This is not forge's `dependencies`, which is a PATH check for commands and
doubles as its `run` allowlist. A shared library is neither a command nor
something a build invokes, so forge cannot express it.

**Declare what somebody might plausibly lack, not everything the binary links.**
`libc`, `libm`, `libstdc++` and `libgcc_s` are on every glibc desktop; declaring
them adds noise and invents failures on a musl system where there is no
`libc.so.6` to find and the program may still run. What a declaration uniquely
buys is a warning *before* a 70MB download, and `hint` — because a soname is not
installable and the package name differs per distribution, which no amount of
inspecting the binary will reveal.

`MissingLibraries` answers from the declaration and needs nothing installed.
`VerifyLinked` asks the dynamic loader about an installed binary, and catches
what a declaration cannot express at all:

```
GLIBCXX_3.4.31 is older than this build needs
GLIBC_2.38 is older than this build needs
libfontconfig.so.1 is missing
```

Those first two are the failure that actually bites: the library is **present**
and too old, a name search says everything is fine, and the program still will
not start. Only the loader knows, because only the loader checks symbol
versions. A missing library is therefore a warning beside a working Install
button and not a disabled one — the download and unpacking succeed either way.

## Two traps worth knowing

**`go:embed` cannot carry an `_itemTitle`.** Embedded paths go through
`module.CheckFilePath`, which allows Unicode letters and digits — `Pokémon` and
`日本` are fine — but only a small set of ASCII punctuation, and U+00B7 MIDDLE
DOT is not in it. A folder named `Gopher64 · gopher64` makes the whole tree
unembeddable, and the build says only `contains no embeddable files`, naming no
file, folder or character. So the embedded tree is keyed by an ASCII slug and
`Materialise` reads the real title out of the item. The
folder-name-equals-identifier invariant holds on disk, where the loaders read;
the embedded copy is a build artifact and no MediaItem store.

**An unsupported machine is not an unsupported OS.** `SelectPlatform` falls back
to the bare OS name, so a program published only for `Mac-arm64` would tell an
Intel Mac "no build for Mac" — which reads as there being no Mac build at all.
Errors name the host and list what is published instead.
