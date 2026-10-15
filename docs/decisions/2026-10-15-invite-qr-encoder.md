# Decision: a QR encoder for the invite link (phase 11 slice 11c)

**Date:** 2026-10-15. **Status:** adopted, measured rather than inherited. **Written before the code
that uses it** — the plan's §11 slice 11c requires the decision to be recorded with what was measured
rather than what a README claims, and this file is that record.

## The need

A member's invite key should render as a QR code they can hand to somebody in person, server-side,
returned as an SVG: it is one algorithm, the output is a text document that needs no image pipeline,
and it keeps the invite path free of a client that must run.

## The candidates

1. **`github.com/skip2/go-qrcode`** — the widely used pure-Go encoder.
2. **`github.com/yeqown/go-qrcode`** — newer, has an SVG writer of its own.
3. **Our own ISO/IEC 18004 byte-mode encoder** — the plan's stated fallback (~200 lines was the
   estimate; Reed-Solomon over GF(256), the version/mask tables and the mask-evaluation penalty
   rules put it closer to 400 with the tables, and it is exactly the kind of code where a subtle
   error produces a code that looks right and scans wrong).

## What was measured (not read)

```
go get github.com/skip2/go-qrcode@latest
  → github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
go mod graph | grep -c skip2
  → 1                      # the module itself: ZERO transitive dependencies
license: MIT (Copyright (c) 2014 Tom Harwood), read from the module cache, not the repository page
imports: bytes, errors, fmt, image, image/color, image/png — standard library only
last upstream commit: 2020-06-17 (a pinned pseudo-version: no tagged releases)
SVG writer: NONE           # ls | grep -i svg → nothing; the package renders matrices, PNG and text
```

And on THIS HOST (the build host), for the verification the plan asks for:

```
which zbarimg rsvg-convert qrencode
  → /usr/bin/zbarimg /usr/bin/rsvg-convert /usr/bin/qrencode
```

**Corrected 2026-10-15, after deploying: those tools are on the BUILD host, not on the deployment host.**
`which zbarimg rsvg-convert` on thinkcentre answers with nothing, so the live walk cannot decode what the
deployment serves and says so instead of reporting a pass (verdict D3: SKIPPED, naming the absent tools and
writing the served SVG to a file). The decode is therefore run WHERE THE TOOLS ARE: the deployed instance's
own SVG, fetched from the host that produced it, decoded here with `rsvg-convert` + `zbarimg` -- which is
stronger evidence than an in-process test, because it is the artefact the running binary actually served.
The Go test keeps the same decode behind a skip, so the suite stays runnable on a host without the tools.

## Decision

**Adopt `github.com/skip2/go-qrcode` for the MATRIX ONLY, pinned at
`v0.0.0-20200617195104-da1b6568686e`, and render the SVG here from its bitmap.**

Why this and not the alternatives:

* The dependency's job is the part where correctness is hard and invisible (Reed-Solomon, the mask
  rules, the version tables). The part this project needs to control is the OUTPUT, and the package
  hands over a `Bitmap()` for exactly that: our SVG is a projection of the matrix, so the artefact
  stays a text document with no image pipeline and no embedded binary, which is the plan's own
  constraint.
* **Zero transitive dependencies** measured, not assumed. Adding a library that drags in an image
  stack to render a square of rectangles would be the opposite trade.
* An unmaintained encoder is an acceptable risk HERE and only here, because the format is frozen and
  the artefact is verifiable against an INDEPENDENT decoder on every run: `zbarimg` reads the rendered
  SVG back (via `rsvg-convert`), and `qrencode` provides a second implementation to compare matrices
  against. A dependency whose output nobody can check is a different risk from one whose output is
  checked by a decoder that shares none of its code.
* `yeqown/go-qrcode` was not chosen because its SVG writer is the part we would rather own (it decides
  the markup and the quiet zone), and owning it removes any reason to take more surface than the
  matrix. Its own transitive set was not measured, because the decision did not need it: the first
  candidate covered the hard part with nothing attached.
* Writing our own was not chosen for the reason the plan anticipated ("if no acceptable Go
  implementation exists" — one does) and for a second reason worth recording: the ~200-line estimate
  in the plan is short, and the failure mode of a hand-rolled encoder is a code that decodes on the
  author's phone. The verification would still be the decoder, so the hand-rolled version's extra risk
  buys nothing the matrix-only adoption does not already give.

## The verification this decision makes possible (and how it is run)

1. Render the SVG from the matrix.
2. `rsvg-convert` the SVG to PNG.
3. `zbarimg --quiet --raw` the PNG and require the payload to be the invite URL, byte for byte.
4. Cross-check the matrix against `qrencode` for the same input.
5. Assert the quiet zone is present (4 modules) — the one thing a matrix-only library leaves to the
   caller and the one that makes a scanner fail for a reason that looks like "the library is broken".

The Go test that runs the decoder SKIPS when `zbarimg` is absent (so the suite stays runnable
anywhere), and the live walk runs it unconditionally on the host where the tool exists.
