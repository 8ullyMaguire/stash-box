#!/usr/bin/env python3
"""Refuse the content plane, mechanically.

The respec of 2026-10-15 made this programme a CATALOGUE OF REFERENCES: it records that a work exists,
who is in it, and the addresses of pages about it. It does not host, embed, proxy, transcode or play
anything, and it does not ship a thumbnail of somebody else's work. That is what keeps an instance in
the category of project that survives a DMCA notice rather than the category that does not, and it is
a property of the TREE and not of anybody's intentions -- so it is checked here, on every gate run,
instead of being written down and trusted.

THE MEDIA PLANE IS NOW SPECIFIED AGAIN, GATED, AND OFF BY DEFAULT (second respec, 2026-10-15, by
owner instruction: "include it in the spec again but disable it by default in config"). This script
is what keeps that restoration honest, and it changed shape rather than being deleted:

  1. The patterns still fail ANYWHERE in the tree except the media plane's own module
     (`internal/service/mediaplane/`). The plane may not arrive as a sprinkle: an `<video>` in
     `internal/api` is a leak, and the module is where a slice that means to serve bytes declares
     itself. The exception list is bounded by a PATH PREFIX, not by a per-case comment.
  2. The switch's shipped default is CHECKED, not assumed: `content_delivery` must exist in
     internal/config with the mapstructure key of that name and a default of false. A capability
     whose default flipped is the exact failure this programme was written to avoid -- an instance
     that serves third-party material is a host, and a host is DMCA'd out of existence.
  3. The switch must be MENTIONED where a process starts, because a key no code reads is a claim
     about a config file rather than a posture (this repository has shipped that shape once).

WHAT A HIT MEANS. Not "this is illegal" and not "somebody did something wrong": it means the tree has
grown a capability outside the module that the spec gates, and the next step is a decision rather than
an edit. Either the feature is refused (delete it), or the spec changes FIRST and the refusal moves
into the spec -- but not into a second script, because a rule in two places is a rule that will
disagree with itself.

ADDING A PATTERN. Add the pattern AND the reason, and make sure the tree is clean of it BEFORE the
pattern lands: a rule with an exception list is a rule nobody reads. `--self-test` proves the scanner
still finds what it is looking for, that the allowlist is narrow, and that the switch check can fail,
because a scanner that silently matched nothing would report the same clean tree as a compliant one.

WHAT THIS DOES NOT COVER, named so the boundary is visible: it is a TEXT scan. A capability reached
through a dependency, a runtime configuration, or a URL that a client resolves elsewhere is invisible
here. It covers the ways a content plane has ever arrived in this repository.
"""
import argparse
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

# Directories worth scanning: the server, the schema, the frontend's source, and the scripts (a
# script that downloads media is the content plane with extra steps).
SCAN_DIRS = ["internal", "graphql", "frontend/src", "cmd", "pkg", "scripts"]

# Extensions that could hold a capability. Python is included: a script that downloads media is the
# content plane with extra steps, and leaving .py out because THIS file is written in Python would
# carve the hole exactly where a helper would be added.
SCAN_EXTS = {".go", ".ts", ".svelte", ".graphql", ".sql", ".js", ".mjs", ".py"}

# Directories that are generated or vendored, and would only ever report somebody else's code.
SKIP_DIRS = {"node_modules", "build", "dist", ".svelte-kit", "vendor", ".git", "__pycache__"}

# The one file that is skipped, and the only kind of exception this script allows: ITSELF. The
# patterns above are written out literally in this file, so scanning it would always report the rule
# as a violation of the rule. A skip for any other reason is the beginning of the exception list the
# module docstring warns against.
SKIP_FILES = {"check-no-content-plane.py"}

# WHERE THE MEDIA PLANE IS ALLOWED TO LIVE, and the whole of the exception list. A path prefix
# rather than a pattern, so the plane is one module a reader can open -- and so a hit outside it is a
# hit, whatever the reason. Adding a second prefix here is a spec change: the spec's second respec
# section names the module, and the switch's default is checked below so the gate cannot be opened by
# editing this list alone.
MEDIA_PLANE_PATHS = ["internal/service/mediaplane/"]

PATTERNS = [
    (r"\biframe\b", "an embedded third-party player: the content plane's front door"),
    (r"<video", "a player element in the tree"),
    (r"\btranscod", "transcoding: turning somebody's file into another file"),
    (r"\bffmpeg\b", "the media toolchain the spec says is not in this repository"),
    (r"\bm3u8\b", "HLS playlists"),
    (r"\bhls\b", "HTTP Live Streaming"),
    (r"\.mp4\b", "a media file reference"),
    (r"content-range", "range requests, i.e. serving bytes of a file to a client"),
    (r"video\.js", "a player library"),
    (r"poster=", "a poster frame, which is a frame of somebody else's video"),
    (r"<source src", "a media source element"),
    (r"accept-ranges", "byte-range serving advertised by this instance"),
    (r"\bwebrtc\b", "media transport"),
    (r"\brtmp\b", "media transport"),
]

# The switch that gates the plane, and where it has to be visible. Kept as data so the self-test can
# feed a mutated config through the SAME check the real tree gets.
SWITCH_FILE = "internal/config/config.go"
SWITCH_FIELD = re.compile(r"ContentDelivery\s+bool\s+`mapstructure:\"content_delivery\"`")
SWITCH_DEFAULT = re.compile(r"^\s*ContentDelivery:\s*(\w+),", re.M)
STARTUP_FILE = "internal/api/server.go"


def relative(path, base=REPO) -> str:
    """Repo-relative when possible, so messages name a file a reader can open."""
    try:
        return str(Path(path).resolve().relative_to(Path(base).resolve()))
    except ValueError:
        return str(path)


def is_media_plane_path(relpath: str) -> bool:
    """True when a path is inside the module the media plane is allowed to occupy."""
    return any(relpath.startswith(prefix) for prefix in MEDIA_PLANE_PATHS)


def scan(roots, base=REPO):
    """Return [(relpath, line_number, line, why)] for every forbidden pattern found."""
    hits = []
    for root in roots:
        root = Path(root)
        if not root.exists():
            continue
        files = [root] if root.is_file() else sorted(root.rglob("*"))
        for path in files:
            if not path.is_file() or path.suffix.lower() not in SCAN_EXTS:
                continue
            if path.name in SKIP_FILES:
                continue
            if any(part in SKIP_DIRS for part in path.parts):
                continue
            try:
                text = path.read_text(encoding="utf-8", errors="replace")
            except OSError:
                continue
            for number, line in enumerate(text.splitlines(), start=1):
                for pattern, why in PATTERNS:
                    if re.search(pattern, line, re.IGNORECASE):
                        hits.append((relative(path, base), number, line.strip(), why))
    return hits


def check_switch(config_text: str, startup_text: str):
    """Return a list of problems with the media-plane switch. Empty means the posture holds.

    Takes the two files' TEXT rather than reading them, so the self-test exercises this exact check
    with mutated sources instead of re-implementing it.
    """
    problems = []

    if not SWITCH_FIELD.search(config_text):
        problems.append(
            f"{SWITCH_FILE} has no `ContentDelivery bool` field with the mapstructure key "
            f"`content_delivery`: the media plane is specified with a switch, and a switch that "
            f"cannot be read from a config file is not one")

    default = SWITCH_DEFAULT.search(config_text)
    if not default:
        problems.append(
            f"{SWITCH_FILE} has no `ContentDelivery:` entry in its defaults, so what an instance "
            f"runs with when no config file names the key is decided by the zero value by accident "
            f"rather than on purpose")
    elif default.group(1) != "false":
        problems.append(
            f"{SWITCH_FILE} defaults content_delivery to {default.group(1)}: every fork would serve "
            f"content without its operator ever deciding, which is the default this gate exists to "
            f"keep off (docs/spec/feature-content-aggregator.md, second respec 2026-10-15)")

    if "logPosture()" not in startup_text:
        problems.append(
            f"{STARTUP_FILE} never calls logPosture(): the switch is then a key nothing mentions at "
            f"runtime, and an operator cannot tell from the log which posture their instance is in")
    if "mediaplane.PostureLine()" not in startup_text:
        problems.append(
            f"{STARTUP_FILE} does not read mediaplane.PostureLine(): the startup line is supposed "
            f"to state the posture, and a line derived from anywhere else can disagree with the gate")

    return problems


def self_test() -> int:
    """Prove the scanner finds planted capabilities, that the allowlist is narrow, and that the
    switch check can fail in both directions."""
    import tempfile

    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)

        clean = tmp_path / "catalogue.go"
        clean.write_text("package catalogue\n\n// This records a url and nothing else.\n")
        planted = tmp_path / "player.go"
        planted.write_text('package player\n\nconst tag = "<video controls>"\n')

        hits = scan([tmp], base=tmp_path)
        if not any(h[0] == "player.go" for h in hits):
            print("SELF-TEST FAILED: a planted <video> was not detected")
            return 1
        if any(h[0] == "catalogue.go" for h in hits):
            print("SELF-TEST FAILED: a clean file reported a hit")
            return 1

        # And the negative direction that matters: the scanner must not match its own pattern list
        # when the file merely MENTIONS the words, or every doc comment about the refusal would be a
        # violation. The scanner does not scan prose about the refusal, so a comment naming `iframe`
        # IS a hit -- which is why the patterns must stay zero in the tree.
        mentioned = tmp_path / "notes.go"
        mentioned.write_text("package notes\n\n// There is no iframe in this tree.\n")
        if not any(h[0] == "notes.go" for h in scan([tmp], base=tmp_path)):
            print("SELF-TEST FAILED: the scanner must be a text scan, including comments -- "
                  "otherwise a capability hidden in a comment-shaped string would pass")
            return 1

        # THE ALLOWLIST ARM: the same capability is tolerated in the module and a violation outside
        # it. Asserted as a PAIR, because a rule that only ever passes (or only ever reports) would
        # pass half of this test and read as working.
        module = tmp_path / MEDIA_PLANE_PATHS[0]
        module.mkdir(parents=True)
        (module / "player.go").write_text('package mediaplane\n\nconst tag = "<video controls>"\n')
        hits = scan([tmp], base=tmp_path)
        inside = [h for h in hits if h[0] == MEDIA_PLANE_PATHS[0] + "player.go"]
        outside = [h for h in hits if h[0] == "player.go"]
        if not inside:
            print("SELF-TEST FAILED: a capability inside the media plane's module was not found at "
                  "all, so the allowlist cannot be what tolerates it")
            return 1
        if not outside:
            print("SELF-TEST FAILED: the same capability OUTSIDE the module is not reported -- the "
                  "allowlist has become an exception list for the whole tree")
            return 1
        if not is_media_plane_path(inside[0][0]) or is_media_plane_path(outside[0][0]):
            print("SELF-TEST FAILED: the classifier disagrees with the paths it was given")
            return 1

        # THE SWITCH ARM: a good pair of files passes, and each of the three ways it can be wrong
        # fails. A check that cannot fail is a check that reports a compliant tree for an empty repo.
        good_config = ('\tContentDelivery bool `mapstructure:"content_delivery"`\n'
                       '\tContentDelivery:            false,\n')
        good_startup = "func logPosture() {\n\tlogger.Infof(\"%s\", mediaplane.PostureLine())\n}\n"
        if check_switch(good_config, good_startup):
            print("SELF-TEST FAILED: the switch check refuses a correct pair of sources")
            return 1

        for label, config_text, startup_text in [
            ("a flipped default",
             good_config.replace("ContentDelivery:            false,",
                                 "ContentDelivery:            true,"), good_startup),
            ("a missing field",
             good_config.replace('ContentDelivery bool `mapstructure:"content_delivery"`', ""),
             good_startup),
            ("a startup line nothing calls",
             good_config, "func other() {}\n"),
        ]:
            if not check_switch(config_text, startup_text):
                print(f"SELF-TEST FAILED: the switch check passes with {label}")
                return 1

    print("SELF-TEST OK: the scanner finds a planted capability, tolerates a clean file, is a text "
          "scan (so comments count), confines the media plane to its module -- asserted inside AND "
          "outside -- and fails on all three ways the switch can be wrong.")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true", help="prove the scanner still works")
    args = parser.parse_args()

    if args.self_test:
        return self_test()

    all_hits = scan([REPO / d for d in SCAN_DIRS])
    plane_hits = [h for h in all_hits if is_media_plane_path(h[0])]
    leaks = [h for h in all_hits if not is_media_plane_path(h[0])]

    switch_problems = check_switch(
        (REPO / SWITCH_FILE).read_text(encoding="utf-8"),
        (REPO / STARTUP_FILE).read_text(encoding="utf-8"))

    if leaks or switch_problems:
        if leaks:
            print("THE CONTENT PLANE HAS ARRIVED OUTSIDE ITS MODULE. The spec gates it: "
                  "docs/spec/feature-content-aggregator.md, second respec of 2026-10-15.")
            print(f"The only place a serving capability may live is {MEDIA_PLANE_PATHS[0]}, and the "
                  f"switch that gates it is off by default.")
            print()
            for path, number, line, why in leaks:
                print(f"  {path}:{number}: {why}")
                print(f"      {line}")
            print()
        if switch_problems:
            print("THE MEDIA PLANE'S SWITCH IS NOT WHAT THE SPEC SAYS IT IS:")
            for problem in switch_problems:
                print(f"  {problem}")
            print()
        print(f"{len(leaks)} leak(s), {len(switch_problems)} switch problem(s). Either delete the "
              f"capability, or change the spec FIRST and move this refusal into it -- not into a "
              f"second exception list here.")
        return 1

    if plane_hits:
        print(f"The media plane's module holds {len(plane_hits)} serving pattern(s) and no leak "
              f"exists anywhere else ({len(PATTERNS)} patterns, {len(SCAN_DIRS)} directories). "
              f"content_delivery is off in the shipped default and the startup line states the "
              f"posture, which is the shape the second respec of 2026-10-15 requires.")
        return 0

    print(f"The content plane is absent from the tree ({len(PATTERNS)} patterns, "
          f"{len(SCAN_DIRS)} directories), the media plane is confined to {MEDIA_PLANE_PATHS[0]}, "
          f"and content_delivery defaults to false and is stated at startup. This is the refusal "
          f"the spec makes, checked.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
