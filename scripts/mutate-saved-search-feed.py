#!/usr/bin/env python3
"""Mutation battery for the saved-search feed (plan phase 13 slice 13b).

Nine probes, one per rule the slice is worth having. Each names the test it must redden, and the battery
FAILS if a probe that should be caught is not -- a mutation that does not build is not a verdict, and a
probe whose named test is not in the run filter is a FALSE SURVIVOR (both lessons were paid for in the
earlier slices, where a `-run` filter that matched nothing reported "ok").

The integration probes need the database the integration suite uses. Run:

    export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable'
    python3 scripts/mutate-saved-search-feed.py [M1 M2 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

FEED_GO = "internal/service/savedsearch/feed.go"
ROUTES = "internal/api/routes_feed.go"
RSS = "internal/feed/rss.go"
SQL_GO = "internal/queries/saved_search.sql.go"

IT = ["go", "test", "-tags=integration", "-count=1", "-v", "-p", "1", "./internal/api/"]
UNIT = ["go", "test", "-count=1", "-v", "./internal/feed/"]


def it(name):
    return IT + ["-run", name]


def unit(name):
    return UNIT + ["-run", name]


MUTATIONS = [
    (
        "M1 the token is stored in the clear instead of its hash",
        FEED_GO,
        "\thash := feedTokenHash(token)\n\taffected, err := s.queries.SetSavedSearchFeedToken(",
        "\thash := token\n\taffected, err := s.queries.SetSavedSearchFeedToken(",
        [it("TestAFeedStoresOnlyTheHashOfItsToken")],
        "'the stored value is the SHA-256 of the token' -- asserted against the database, not the code",
    ),
    (
        # The owner is not decoration: the query path reads the current user, so a feed run with nobody in
        # the context is not imprecise, it fails.
        "M2 the search is run with nobody in the context",
        ROUTES,
        "\trunCtx := context.WithValue(r.Context(), auth.ContextUser, search.Owner)",
        # A typed nil rather than deleting the call: the first version of this probe removed the
        # WithValue and left `auth` imported and unused, so the package did not build -- a COMPILE ERROR
        # is not a verdict, and the battery reported it as such instead of counting it.
        "\trunCtx := context.WithValue(r.Context(), auth.ContextUser, (*auth.AuthUser)(nil))",
        [it("TestAFeedCarriesTheSameRecordsTheSearchDoes")],
        "the feed must be the OWNER's view, so its absence has to be observable",
    ),
    (
        "M3 a miss that is not a 404 is answered differently",
        ROUTES,
        "\t\thttp.NotFound(w, r)\n\t\treturn\n\t}\n\n\t// THE SEARCH RUNS AS ITS OWNER.",
        "\t\thttp.Error(w, \"no\", http.StatusGone)\n\t\treturn\n\t}\n\n\t// THE SEARCH RUNS AS ITS OWNER.",
        [it("TestEveryFeedThatDoesNotResolveIsTheSame404")],
        "'must be indistinguishable from the others' -- the body too, not only the status",
    ),
    (
        "M4 the links are built without the address the request arrived at",
        ROUTES,
        "\tbase := baseURL(r.Context())",
        "\tbase := \"\"",
        [it("TestAFeedCarriesTheSameRecordsTheSearchDoes")],
        "the item's link, which a reader has to be able to open",
    ),
    (
        # The URL is the credential, so this header is not decoration: without it a reader sends the token
        # to every site it links to.
        "M5 the feed leaks its URL as a referrer",
        ROUTES,
        "\tw.Header().Set(\"Referrer-Policy\", \"no-referrer\")\n",
        "",
        [it("TestAFeedIsNotIndexedNotSharedAndLeaksNoReferrer")],
        "'a reader clicking an item would otherwise send the token to wherever the item points'",
    ),
    (
        "M6 disabling a feed does not clear the token",
        SQL_GO,
        "UPDATE saved_searches SET feed_token_hash = NULL, updated_at = now()",
        "UPDATE saved_searches SET feed_token_hash = feed_token_hash, updated_at = now()",
        [it("TestDisablingAFeedKillsItsURLAndEnablingAgainRotatesIt")],
        "'the hash is cleared, so the URL can never resolve again'",
    ),
    (
        "M7 an empty feed says nothing about why it is empty",
        ROUTES,
        '\t\treturn description + " (no matches right now)"',
        "\t\treturn description",
        [it("TestAFeedThatMatchesNothingIsStillAFeed")],
        "'an empty feed is not indistinguishable from a broken one'",
    ),
    (
        # The Go trap this test is named for: RFC1123 writes the word "UTC" where RSS wants a numeric zone.
        "M8 the pubDate uses a format a parser may not read",
        RSS,
        "return t.UTC().Format(time.RFC1123Z)",
        "return t.UTC().Format(time.RFC1123)",
        [unit("TestPubDateCarriesANumericZoneAndNeverTheWordUTC")],
        "the date format, which is how a whole feed disappears without an error anywhere",
    ),
    (
        "M9 the guid is declared to be the link",
        RSS,
        "GUID:        GUID{IsPermaLink: false, Value: guid},",
        "GUID:        GUID{IsPermaLink: true, Value: guid},",
        [unit("TestTheGUIDIsTheRecordAndNotTheLink")],
        "'the identity is not the address'",
    ),
]


def run(cmd):
    env = dict(os.environ)
    proc = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=1800, env=env)
    return proc.returncode, proc.stdout + proc.stderr


def verdict(out):
    # A panic counts as RED: it is the failure mode of the probe that runs the search with nobody in the
    # context, and a crash reported as "survived" would be the same mistake this battery exists to catch.
    if "build failed" in out or "[build failed]" in out or "declared and not used" in out:
        return "BROKEN"
    if "--- FAIL" in out or "panic:" in out or "\nFAIL" in out:
        return "RED"
    return "GREEN"


def main():
    only = [a for a in sys.argv[1:] if not a.startswith("-")]
    results = []

    for name, path, anchor, repl, cmds, expects in MUTATIONS:
        if only and not any(name.startswith(o) for o in only):
            continue

        full = os.path.join(REPO, path)
        with open(full) as f:
            original = f.read()

        hits = original.count(anchor)
        if hits != 1:
            print(f"!! {name}: anchor matched {hits} times, SKIPPING (not a verdict)")
            results.append((name, "ANCHOR-MISS", expects))
            continue

        with open(full, "w") as f:
            f.write(original.replace(anchor, repl))

        verdicts = []
        try:
            for cmd in cmds:
                _, out = run(cmd)
                v = verdict(out)
                verdicts.append(v)
                if v == "RED":
                    reds = [l.strip() for l in out.splitlines() if l.strip().startswith("--- FAIL")]
                    print(f"   red: {'; '.join(reds[:3]) or 'panic'}")
                if v == "GREEN":
                    print("   green -- NOTHING NOTICED")
                if v == "BROKEN":
                    print(f"   BUILD BROKEN, not a verdict: {out.strip().splitlines()[-1][:160]}")
        finally:
            with open(full, "w") as f:
                f.write(original)
            assert open(full).read() == original, "RESTORE FAILED"

        if "BROKEN" in verdicts:
            overall = "BROKEN"
        elif all(v == "RED" for v in verdicts):
            overall = "KILLED"
        else:
            overall = "SURVIVED"

        print(f"{overall:8} {name}\n         expects: {expects}")
        results.append((name, overall, expects))

    print("\n==== summary ====")
    for name, v, _ in results:
        print(f"{v:12} {name}")
    killed = sum(1 for _, v, _ in results if v == "KILLED")
    broken = [n for n, v, _ in results if v in ("BROKEN", "ANCHOR-MISS")]
    print(f"\n{killed}/{len(results)} killed" + (f"; NOT VERDICTS: {broken}" if broken else ""))
    return 0 if killed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
