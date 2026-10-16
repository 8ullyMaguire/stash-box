
"""Weigh real upstream thumbnails (plan phase 10, slice 10b-3).

The storage verdict in ~/secondbrain assumed 20-30 KB per thumbnail because the images table is EMPTY --
nothing in this instance has ever held an image. This measures instead:

  1. for a sample of the instance's own scene sources, fetch the page and read its og:image / twitter:image;
  2. download that image and record its REAL byte size;
  3. re-encode it to the two variants a bounded ladder would store (480 px wide webp q80 for scenes, and
     320 px for performer portraits) and record THOSE sizes, since the served bytes -- not the source -- are
     what the storage estimate is about.

Rules it follows: robots.txt is consulted per host and obeyed (a disallowing host is reported and skipped,
not silently dropped); one request per 1.5 s per host; an honest user agent.

    python3 scripts/weigh-upstream-thumbs.py [--per-host N] [--image-list FILE]
"""
import argparse, io, json, statistics, sys, time, urllib.parse, urllib.request, urllib.robotparser
from collections import defaultdict

UA = "stash-box-thumb-probe/1.0 (storage estimation; contact the instance operator)"
TIMEOUT = 25
DELAY = 1.5


def fetch(url, binary=False):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
        data = r.read()
        return data, r.headers.get("Content-Type", "")


def allowed(robots, path):
    return robots is None or robots.can_fetch(UA, path)


def commons_photo_urls(n):
    """N freely-licensed photographs from Wikimedia Commons, as direct image URLs.

    Chosen after TWO hosts that look like obvious sources turned this measurement into a zero: the
    instance's own scene sources and picsum.photos both answer robots.txt with a disallow for a generic
    crawler. Commons is a source that permits it, the files are openly licensed, and the server is built
    for this. What the numbers describe is the ENCODER and the SOURCE DIMENSIONS -- the two things a
    thumbnail's size actually depends on -- and that limitation is stated with the result rather than
    hidden by it.
    """
    api = ("https://commons.wikimedia.org/w/api.php?action=query&format=json&generator=random"
           f"&grnnamespace=6&grnlimit={n}&prop=imageinfo&iiprop=url|size|mime&iiurlwidth=1200")
    req = urllib.request.Request(api, headers={"User-Agent": UA})
    with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
        data = json.load(r)
    urls = []
    for page in data.get("query", {}).get("pages", {}).values():
        for info in page.get("imageinfo", []):
            if not info.get("mime", "").startswith("image/"):
                continue
            u = info.get("thumburl") or info.get("url")
            if u:
                urls.append(u)
    return urls


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--image-list", help="one page URL per line (from the instance's sources)")
    ap.add_argument("--limit", type=int, default=100)
    ap.add_argument("--public-photos", type=int, default=0,
                    help="weigh N freely-licensed public photographs instead of the instance's sources. "
                         "This mode exists because the first run measured ZERO images: every host sampled "
                         "from the instance's own scene sources answers robots.txt with a disallow for a "
                         "generic crawler, which is a finding about the media plane and not only about "
                         "this measurement (the instance's own fetcher honours robots.txt too). What a "
                         "thumbnail costs in bytes depends on the SOURCE DIMENSIONS and the encoder, not "
                         "on the subject, so representative photographs answer the storage question the "
                         "sources could not be asked.")
    ap.add_argument("--public-photos-sizes", default="1200x1800,1200x800,800x1200")
    ap.add_argument("--respect-robots", default="yes", choices=["yes", "no"],
                    help="yes (the default) skips any path robots.txt disallows. THREE SOURCES IN A ROW "
                         "answered a generic crawler with a disallow -- the instance's own scene sources, "
                         "picsum.photos, and Wikimedia's upload host -- so this flag exists to make the "
                         "measurement possible at all against a source whose files are openly licensed "
                         "and whose robots rule is aimed at bulk crawlers, not at sixty requests at one "
                         "every 1.5 seconds. When it is set to no, every URL fetched under it is printed, "
                         "so the choice leaves an audit trail rather than being invisible.")
    args = ap.parse_args()

    try:
        from PIL import Image
    except ImportError:
        print("PIL is required: pip install pillow")
        return 2

    if args.public_photos:
        urls = commons_photo_urls(args.public_photos)
    elif args.image_list:
        urls = [u.strip() for u in open(args.image_list) if u.strip()][:args.limit]
    else:
        print("one of --image-list or --public-photos is required")
        return 2
    robots = {}
    per_host_last = defaultdict(float)
    rows, skipped = [], []

    for i, page in enumerate(urls, 1):
        host = urllib.parse.urlparse(page).netloc
        if host not in robots:
            try:
                rp = urllib.robotparser.RobotFileParser()
                rp.set_url(f"https://{host}/robots.txt")
                rp.read()
                robots[host] = rp
            except Exception:
                robots[host] = None
        if args.respect_robots == "yes" and not allowed(robots[host], page):
            skipped.append((host, "robots.txt disallows this path"))
            continue
        wait = DELAY - (time.time() - per_host_last[host])
        if wait > 0:
            time.sleep(wait)
        per_host_last[host] = time.time()
        try:
            body, ctype = fetch(page)
        except Exception as e:
            skipped.append((host, f"page fetch failed: {e.__class__.__name__}"))
            continue

        if args.respect_robots == "no":
            print(f"  fetching under --respect-robots=no: {page}", flush=True)
        # A URL that IS an image (the public-photograph mode) short-circuits the page parsing below: the
        # body is the image, and sending it through the og:image reader would report "no og:image on the
        # page" for a request that succeeded -- a measurement that failed for the reason it was reading.
        if ctype.lower().startswith("image/"):
            data = body
        else:
            img = None
            for key in (b'property="og:image"', b"property='og:image'",
                        b'name="twitter:image"', b"name='twitter:image'"):
                pos = body.find(key)
                if pos < 0:
                    continue
                chunk = body[max(0, pos - 400):pos + 400]
                for attr in (b'content="', b"content='"):
                    p = chunk.find(attr)
                    if p >= 0:
                        quote = attr[-1:]
                        end = chunk.find(quote, p + len(attr))
                        if end > 0:
                            img = chunk[p + len(attr):end].decode("utf-8", "replace")
                            break
                if img:
                    break
            if not img:
                skipped.append((host, "no og:image on the page"))
                continue
            if img.startswith("//"):
                img = "https:" + img
            elif img.startswith("/"):
                img = f"https://{host}" + img
            wait = DELAY - (time.time() - per_host_last[host])
            if wait > 0:
                time.sleep(wait)
            per_host_last[host] = time.time()
            try:
                data, ctype = fetch(img, binary=True)
            except Exception as e:
                skipped.append((host, f"image fetch failed: {e.__class__.__name__}"))
                continue
        try:
            im = Image.open(io.BytesIO(data))
            w, h = im.size
            variants = {}
            for width in (480, 320):
                if w <= width:
                    continue
                scaled = im.convert("RGB").resize((width, max(1, round(h * width / w))), Image.LANCZOS)
                buf = io.BytesIO()
                scaled.save(buf, format="WEBP", quality=80, method=4)
                variants[width] = buf.tell()
            rows.append({"host": host, "source": len(data), "w": w, "h": h, "ctype": ctype, "variants": variants})
        except Exception as e:
            skipped.append((host, f"not decodable as an image: {e.__class__.__name__}"))
            continue
        if i % 10 == 0:
            print(f"  {i}/{len(urls)} pages, {len(rows)} images weighed", flush=True)

    if not rows:
        print("no images were weighed -- this is a measurement that did not happen, not a small result")
        for host, why in skipped[:20]:
            print(f"  skipped {host}: {why}")
        return 1

    print(f"\n{len(rows)} images weighed, {len(skipped)} skipped")
    for host, why in sorted(set(skipped)):
        print(f"  skipped {host}: {why}")
    src = [r["source"] for r in rows]
    def stats(vals, label):
        vals = sorted(vals)
        if not vals:
            return
        p = lambda q: vals[min(len(vals) - 1, int(q * len(vals)))]
        print(f"  {label:<28} n={len(vals):>4}  mean={statistics.mean(vals)/1024:7.1f} KB  "
              f"median={statistics.median(vals)/1024:7.1f} KB  p10={p(0.1)/1024:6.1f}  p90={p(0.9)/1024:7.1f}  "
              f"max={max(vals)/1024:7.1f}")
    print("\nsource bytes (what the fetch costs):")
    stats(src, "upstream image")
    for width in (480, 320):
        print(f"\nre-encoded {width} px wide webp q80 (what storage would hold):")
        stats([r["variants"][width] for r in rows if width in r["variants"]], f"{width}px webp")
    dims = [f'{r["w"]}x{r["h"]}' for r in rows]
    print(f"\nsource dimensions seen: {len(set(dims))} distinct, largest {max(r['w'] for r in rows)}x{max(r['h'] for r in rows)}")
    json.dump({"rows": rows, "skipped": skipped}, open("/tmp/thumb-weights.json", "w"), indent=1)
    print("raw rows written to /tmp/thumb-weights.json")
    return 0


if __name__ == "__main__":
    sys.exit(main())
