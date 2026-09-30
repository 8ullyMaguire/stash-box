# Mutation harnesses

These are the mutation obligation for the R074 / D2 work, committed so the claims
in `SPEC.md` §7.23.1a and `WORKLOG.md` can be **re-verified** rather than trusted.

A passing test suite says the tests agree with the code. It does not say the tests
would notice if the code were wrong. These specs mutate the code deliberately and
report which mutations the suite caught.

## Running one

```bash
cd ~/code-local/worktrees/stash-box-r074
python3 docs/track/mutations/mutate_r074.py "$PWD" docs/track/mutations/<name>.json
```

Output is one line per mutation — `KILLED` (the suite noticed) or `SURVIVED` (it
did not) — and the process exits non-zero if any mutation is not applied, so a
stale `old` string is reported rather than silently counted as a pass.

**A spec whose `old` text no longer matches the source will report
`NOT APPLIED`.** That is the harness refusing to lie: a mutation that does not
land cannot kill anything, and scoring it as a pass would inflate the count.
Re-target the spec before trusting its result.

## What "SURVIVED" means here

Across all ten specs, **every survivor was a defect in a test or dead code — never
an unfixed hole in the shipped behaviour.** That is the rule this work produced:

> A surviving mutation means the guarded line is **dead or redundant**. Delete it;
> do not write a test to pin it.

Three survivors worth reading up in `WORKLOG.md`, because each would have been
"fixed" wrongly:

- `dialguard_test.go` drove a local **copy** of the rule, so two mutations gutting
  the real function were invisible. A test of a copy proves the copy.
- A fake resolver returned one fixed **public** address for every host, so
  `http://127.0.0.1` resolved outward and the guard allowed it. A fake that ignores
  its argument is worse than no fake.
- `imageurl.go` reused `IsSuspiciousValue`, the question-text predicate, which
  rejects any value containing `http://`. An image url is a url — the marker LIST
  is shareable, the PREDICATE is not.

## The specs

| Spec | Mutations | Guards |
|---|---|---|
| `r074-mutations.json` | 13 | the base-URL guard and the migration scanner |
| `r074-wiring-mutations.json` | 7 | the guard actually being called from Create/Update |
| `708-mutations.json` | 11 | the configurable edit-authority trust threshold |
| `federation-surface-mutations.json` | 11 | the GraphQL operator surface reaching the guard |
| `f2-boundary-mutations.json` | 8 | a foreign candidate reaching the local vote path |
| `dialguard-mutations.json` | 7 | the dial-time half of rule 2 |
| `imageurl-mutations.json` | 9 | rule 3 on `images.url`, including over-strict mutations |
| `foreign-candidates-mutations.json` | 8 | F2 asserted on the GraphQL type, not the resolver |
| `client-mutations.json` | 8 | the F1 content guard and the dial-time call site |
| `issue9-mutations.json` | 3 | the NULL-update defect, asserted as present |

Three specs carry mutations that are deliberately **over-strict** — the guard must
not refuse a dotted host, must not use the question predicate, must not reject a
host that looks unreachable from here. On a guard whose failure mode is destroying
existing user data, those are the mutations that matter most.

`foreign-candidates-mutations.json` contains one mutation the harness cannot score:
renaming the type breaks `gqlgen` before the test runs. That premise was checked
directly instead — with the type deleted the test fails with *"ForeignCandidate is
not in the schema at all"* rather than passing vacuously.