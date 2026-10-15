#!/usr/bin/env python3
"""Refuse the content plane, mechanically.

The respec of 2026-10-15 made this programme a CATALOGUE OF REFERENCES: it records that a work exists,
who is in it, and the addresses of pages about it. It does not host, embed, proxy, transcode or play
anything, and it does not ship a thumbnail of somebody else's work. That is what keeps an instance in
the category of project that survives a DMCA notice rather than the category that does not, and it is
a property of the TREE and not of anybody's intentions -- so it is checked here, on every gate run,
instead of being written down and trusted.

WHAT A HIT MEANS. Not "this is illegal" and not "somebody did something wrong": it means the tree has
grown a capability the spec refuses, and the next step is a decision rather than an edit. Either the
feature is refused (delete it), or the spec changes first and the refusal moves into the spec -- but
not into a second script, because a rule in two places is a rule that will disagree with itself.

ADDING A PATTERN. Add the pattern AND the reason, and make sure the tree is clean of it BEFORE the
pattern lands: a rule with an exception list is a rule nobody reads. `--self-test` proves the scanner
still finds what it is looking for, because a scanner that silently matched nothing would report the
same clean tree as a compliant one.

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


def scan(roots):
    """Return [(path, line_number, line, why)] for every forbidden pattern found."""
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
                        hits.append((str(path), number, line.strip(), why))
    return hits


def self_test() -> int:
    """Prove the scanner finds a planted capability and tolerates a clean file."""
    import tempfile

    with tempfile.TemporaryDirectory() as tmp:
        planted = Path(tmp) / "player.go"
        planted.write_text('package player\n\nconst tag = "<video controls>"\n')
        clean = Path(tmp) / "catalogue.go"
        clean.write_text('package catalogue\n\n// This records a url and nothing else.\n')

        hits = scan([tmp])
        found = [h for h in hits if h[0].endswith("player.go")]
        if not found:
            print("SELF-TEST FAILED: a planted <video> was not detected")
            return 1
        if any(h[0].endswith("catalogue.go") for h in hits):
            print("SELF-TEST FAILED: a clean file reported a hit")
            return 1

        # And the negative direction that matters: the scanner must not match its own pattern list
        # when the file merely MENTIONS the words, or every doc comment about the refusal would be a
        # violation. The scanner does not scan prose about the refusal, so a comment naming `iframe`
        # IS a hit -- which is why the patterns must stay zero in the tree.
        mentioned = Path(tmp) / "notes.go"
        mentioned.write_text('package notes\n\n// There is no iframe in this tree.\n')
        if not any(h[0].endswith("notes.go") for h in scan([tmp])):
            print("SELF-TEST FAILED: the scanner must be a text scan, including comments -- "
                  "otherwise a capability hidden in a comment-shaped string would pass")
            return 1

    print("SELF-TEST OK: the scanner finds a planted capability, tolerates a clean file, and is a "
          "text scan (so comments count, which is why the tree is kept at zero hits)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true", help="prove the scanner still works")
    args = parser.parse_args()

    if args.self_test:
        return self_test()

    hits = scan([REPO / d for d in SCAN_DIRS])
    if not hits:
        print(f"The content plane is absent from the tree ({len(PATTERNS)} patterns, "
              f"{len(SCAN_DIRS)} directories). This is the refusal the spec makes, checked.")
        return 0

    print("THE CONTENT PLANE HAS ARRIVED. The spec refuses it (docs/spec/feature-content-aggregator.md,")
    print("respec of 2026-10-15): this programme catalogues references and serves no media.")
    print()
    for path, number, line, why in hits:
        print(f"  {path}:{number}: {why}")
        print(f"      {line}")
    print()
    print(f"{len(hits)} occurrence(s). Either delete the capability, or change the spec FIRST and")
    print("move this refusal into it -- not into a second exception list here.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
