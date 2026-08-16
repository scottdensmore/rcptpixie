#!/usr/bin/env python3
"""Generate photographed-looking receipts with known ground truth.

The committed fixtures are clean renders, which measure the text path well and
the vision path not at all. This draws thermal-till content and then degrades it
the way a phone photo of paper does — perspective from an off-axis camera, a
lighting gradient and specular hot spot, the curl of a receipt lying on a desk,
uneven thermal fade, defocus, sensor noise and JPEG compression — so the vision
path can be measured without anyone handing over their real receipts.

Content is procedural, so a new seed is a new corpus rather than the same ten
receipts again. It writes truth.json next to the images, which is exactly what
the eval harness reads:

    python3 scripts/genphotos/genphotos.py --out testdata/real --count 24
    go test -tags eval ./internal/analyze -run TestRealCorpus -eval.corpus=testdata/real -v

Requires Pillow and numpy (see requirements.txt); neither touches the Go build.
"""

from __future__ import annotations

import argparse
import json
import math
import os
import random

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

FONT_DIRS = [
    "/usr/share/fonts/truetype/dejavu",
    "/usr/share/fonts/dejavu",
    "/Library/Fonts",
    "/System/Library/Fonts/Supplemental",
]

# Degradation per tier.
#         angle  persp  blur  noise  jpeg  fade  curl  light
TIERS = {
    "clean": (2.0, 0.010, 1.0, 3.0, 88, 0.06, 0.010, 0.16),
    "typical": (6.0, 0.035, 2.4, 6.0, 68, 0.16, 0.030, 0.34),
    "rough": (11.0, 0.070, 4.5, 11.0, 45, 0.28, 0.060, 0.52),
}

US_VENDORS = [
    ("CORNER MARKET", "1180 Ashby Avenue", "Groceries"),
    ("BLUE BOTTLE COFFEE", "Ferry Building", "Food"),
    ("TRADER JOE'S #118", "Masonic Ave", "Groceries"),
    ("SHELL STATION 4471", "200 Cleveland Ave", "Fuel"),
    ("WELLSPRING PHARMACY", "44 Elm Street", "Medical"),
    ("GOLDEN NOODLE BAR", "88 Grant Avenue", "Food"),
    ("CITY HARDWARE", "441 Bridge Road", "Office"),
    ("AIRPORT PARKING", "Lot C  Space 219", "Transportation"),
    ("REDWOOD BOOKSHOP", "12 Juniper Way", "Shopping"),
    ("SUNSET LAUNDROMAT", "901 Irving St", "Other"),
]

EU_VENDORS = [
    ("CAFE MOZART", "Albertinaplatz 2, Wien", "Food"),
    ("RESTAURANT LE JULES VERNE", "Tour Eiffel, Paris", "Food"),
    ("BUCHHANDLUNG THALIA", "Mariahilfer Str, Wien", "Shopping"),
    ("SUPERMERCADO DIA", "Calle Mayor, Madrid", "Groceries"),
    ("TRATTORIA DA ENZO", "Via dei Vascellari, Roma", "Food"),
]

HOTELS = [
    ("THE GRAND HOTEL", "500 Market Street, Chicago IL", False),
    ("SEASIDE INN", "22 Harbour Road, Brighton", True),
    ("HOTEL ADLON", "Unter den Linden, Berlin", True),
    ("THE PALMS RESORT", "1400 Ocean Drive, Miami FL", False),
]

ITEMS = [
    ("MILK 2%", 2.29, 4.99), ("SOURDOUGH", 2.50, 6.50), ("BANANAS 1.2kg", 1.10, 2.80),
    ("OAT MILK", 2.19, 4.29), ("DARK CHOCOLATE", 2.99, 6.99), ("HUMMUS", 2.49, 5.49),
    ("LATTE", 3.75, 6.25), ("CROISSANT", 2.95, 4.95), ("ESPRESSO", 2.20, 3.60),
    ("CLAW HAMMER", 12.00, 26.00), ("NAILS 2in BOX", 3.50, 7.50), ("DUCT TAPE", 4.25, 9.75),
    ("PRESCRIPTION", 8.00, 64.00), ("VITAMIN D3", 6.99, 14.99), ("PLASTERS", 2.49, 5.99),
    ("BEEF NOODLE", 12.50, 19.50), ("DUMPLINGS 12PC", 9.00, 15.00), ("TEA POT", 3.00, 6.00),
    ("PAPERBACK", 8.99, 22.00), ("WASH & FOLD", 11.00, 28.00),
]

# (format string, numeric day/month?) — a numeric format is only *actually*
# ambiguous when the day could pass for a month, which is what ambiguous()
# decides. Ambiguity is the thing worth measuring: a day/month swap is the
# dominant date failure and no amount of image quality fixes it.
US_DATE_FORMATS = [
    ("%m/%d/%Y", True), ("%m-%d-%Y", True), ("%b %d, %Y", False),
    ("%B %d, %Y", False), ("%Y-%m-%d", False), ("%m/%d/%y", True),
]
EU_DATE_FORMATS = [
    ("%d/%m/%Y", True), ("%d.%m.%Y", True), ("%d %b %Y", False),
    ("%Y-%m-%d", False), ("%d.%m.%y", True),
]


def ambiguous(numeric: bool, day) -> bool:
    """True when swapping day and month would still read as a valid, different date."""
    return numeric and day.day <= 12 and day.day != day.month


def find_font(*names: str) -> str:
    for d in FONT_DIRS:
        for n in names:
            p = os.path.join(d, n)
            if os.path.exists(p):
                return p
    raise SystemExit(
        "no usable monospace font found; looked for DejaVuSansMono in " + ", ".join(FONT_DIRS)
    )


def money(rng: random.Random, lo: float, hi: float) -> float:
    return round(rng.uniform(lo, hi), 2)


def fmt_amount(v: float, euro: bool, symbol: str = "") -> str:
    """Real receipts print a currency symbol; omitting it left the corpus with no
    locale evidence at all, which is not a case a real receipt presents."""
    if euro:
        s = f"{v:,.2f}".replace(",", "\x00").replace(".", ",").replace("\x00", ".")
        return f"{s} {symbol}".strip()
    return f"{symbol}{v:,.2f}"


def compose(rng: random.Random, idx: int) -> tuple[list[tuple[str, int]], dict, str]:
    """Build one receipt's lines, its ground truth, and a note about the date."""
    import datetime as dt

    kind = rng.choices(["shop", "hotel"], weights=[5, 1])[0]
    euro = rng.random() < 0.3
    day = dt.date(rng.randint(2023, 2025), rng.randint(1, 12), rng.randint(1, 28))

    if kind == "hotel":
        name, addr, eu_hotel = rng.choice(HOTELS)
        euro = eu_hotel
        fmts = EU_DATE_FORMATS if euro else US_DATE_FORMATS
        f, numeric = rng.choice(fmts)
        nights = rng.randint(1, 6)
        out = day + dt.timedelta(days=nights)
        sym = "EUR" if euro else "$"
        rate = money(rng, 95, 420)
        room = round(rate * nights, 2)
        tax = round(room * rng.uniform(0.08, 0.16), 2)
        total = round(room + tax, 2)
        lines = [
            (name, 1), (f" {addr}", 0), ("", 0),
            (f"Check-In : {day.strftime(f)}", 0),
            (f"Check-Out: {out.strftime(f)}", 0),
            ("-" * 26, 0),
            (f"ROOM {nights} x {fmt_amount(rate, euro, sym)}", 0),
            (f"TAXES{fmt_amount(tax, euro, sym):>21}", 0),
            ("-" * 26, 0),
            (f"TOTAL{fmt_amount(total, euro, sym):>21}", 1),
        ]
        truth = {
            "vendor": name.split()[0] if len(name.split()) < 3 else " ".join(name.split()[:2]),
            "date": day.isoformat(), "end_date": out.isoformat(), "total": total,
        }
        amb = ambiguous(numeric, day) or ambiguous(numeric, out)
        note = f"printed {day.strftime(f)} .. {out.strftime(f)}" + (" (ambiguous)" if amb else "")
        return lines, truth, note

    pool = EU_VENDORS if euro else US_VENDORS
    name, addr, _ = rng.choice(pool)
    fmts = EU_DATE_FORMATS if euro else US_DATE_FORMATS
    f, numeric = rng.choice(fmts)

    sym = rng.choice(["EUR", "\u20ac"]) if euro else "$"
    picks = rng.sample(ITEMS, rng.randint(2, 5))
    rows, subtotal = [], 0.0
    for label, lo, hi in picks:
        v = money(rng, lo, hi)
        subtotal += v
        rows.append((f"{label:<16}{fmt_amount(v, euro, sym):>11}", 0))
    subtotal = round(subtotal, 2)
    tax = round(subtotal * rng.uniform(0.0, 0.11), 2)
    total = round(subtotal + tax, 2)

    lines = [(f"*** {name} ***" if rng.random() < 0.4 else name, 1), (f" {addr}", 0)]
    if rng.random() < 0.5:
        lines.append((f" TEL 555-{rng.randint(1000, 9999)}", 0))
    lines += [("", 0), (f"{day.strftime(f)}   {rng.randint(8, 21):02d}:{rng.randint(0, 59):02d}", 0), ("-" * 26, 0)]
    lines += rows
    lines.append(("-" * 26, 0))
    if tax > 0:
        lines.append((f"SUBTOTAL{fmt_amount(subtotal, euro, sym):>18}", 0))
        lines.append((f"TAX{fmt_amount(tax, euro, sym):>23}", 0))
    lines.append((f"TOTAL{fmt_amount(total, euro, sym):>21}", 1))
    if rng.random() < 0.6:
        lines += [("", 0), (f"VISA ****{rng.randint(1000, 9999)}  APPROVED", 0)]

    truth = {"vendor": " ".join(name.replace("*", "").split()[:2]).strip(), "date": day.isoformat(), "total": total}
    note = f"printed {day.strftime(f)}" + (" (ambiguous)" if ambiguous(numeric, day) else "")
    return lines, truth, note


def render_receipt(lines, mono, mono_b, width=1240):
    pad, lh = 62, 64
    img = Image.new("L", (width, pad * 2 + lh * len(lines)), 250)
    d = ImageDraw.Draw(img)
    f = ImageFont.truetype(mono, 40)
    fb = ImageFont.truetype(mono_b, 43)
    y = pad
    for text, bold in lines:
        font = fb if bold else f
        d.text(((width - d.textlength(text, font=font)) / 2, y), text, font=font, fill=35 if bold else 60)
        y += lh
    return img


def fade_thermal(img, amount, nprng):
    a = np.asarray(img).astype(np.float32)
    h, _ = a.shape
    band = np.linspace(0, 1, h)[:, None]
    wobble = 0.5 + 0.5 * np.sin(band * math.pi * nprng.uniform(1.5, 3.5) + nprng.uniform(0, 3))
    return Image.fromarray(np.clip(255.0 - (255.0 - a) * (1.0 - amount * wobble), 0, 255).astype(np.uint8))


def curl(img, amount, rng):
    a = np.asarray(img).astype(np.float32)
    h, w = a.shape
    shift = (amount * w) * np.sin(np.arange(h) / h * math.pi * rng.uniform(1.0, 2.2) + rng.uniform(0, 3))
    xx = np.arange(w)
    out = np.empty_like(a)
    for y in range(h):
        out[y] = np.interp(xx, xx + shift[y], a[y], left=a[y][0], right=a[y][-1])
    return Image.fromarray(np.clip(out, 0, 255).astype(np.uint8))


def perspective(img, k, rng):
    w, h = img.size
    dx, dy = k * w, k * h
    src = [(0, 0), (w, 0), (w, h), (0, h)]
    dst = [
        (rng.uniform(0, dx), rng.uniform(0, dy)),
        (w - rng.uniform(0, dx), rng.uniform(0, dy)),
        (w - rng.uniform(0, dx), h - rng.uniform(0, dy)),
        (rng.uniform(0, dx), h - rng.uniform(0, dy)),
    ]
    A, B = [], []
    for (sx, sy), (ux, uy) in zip(src, dst):
        A.append([ux, uy, 1, 0, 0, 0, -sx * ux, -sx * uy])
        A.append([0, 0, 0, ux, uy, 1, -sy * ux, -sy * uy])
        B += [sx, sy]
    coeffs = np.linalg.solve(np.array(A, dtype=np.float64), np.array(B, dtype=np.float64))
    return img.transform((w, h), Image.PERSPECTIVE, coeffs, Image.BICUBIC, fillcolor=255)


def lighting(img, amount, rng):
    a = np.asarray(img).astype(np.float32)
    h, w = a.shape
    yy, xx = np.mgrid[0:h, 0:w]
    ang = rng.uniform(0, 2 * math.pi)
    grad = math.cos(ang) * xx / w + math.sin(ang) * yy / h
    grad = (grad - grad.min()) / (grad.max() - grad.min() + 1e-6)
    cx, cy = rng.uniform(0.2, 0.8) * w, rng.uniform(0.1, 0.9) * h
    r = math.hypot(w, h) * rng.uniform(0.18, 0.34)
    hot = np.exp(-(((xx - cx) ** 2 + (yy - cy) ** 2) / (2 * r * r)))
    return Image.fromarray(np.clip(a * (1.0 - amount * grad + amount * 0.7 * hot), 0, 255).astype(np.uint8))


def photograph(paper, tier, seed):
    rng = random.Random(seed)
    nprng = np.random.default_rng(seed)
    angle, persp, blur, noise, jpeg, fadeamt, curlamt, light = TIERS[tier]

    img = fade_thermal(paper, fadeamt * rng.uniform(0.7, 1.3), nprng)
    img = curl(img, curlamt * rng.uniform(0.6, 1.4), rng)

    pw, ph = img.size
    W, H = int(pw * rng.uniform(1.30, 1.55)), int(ph * rng.uniform(1.12, 1.30))
    desk = rng.randint(95, 165)
    scene_a = np.full((H, W), float(desk)) + nprng.normal(0, 6, (H, W))
    scene = Image.fromarray(np.clip(scene_a, 0, 255).astype(np.uint8))
    ox, oy = (W - pw) // 2, (H - ph) // 2
    sh = Image.new("L", (pw + 18, ph + 18), 0)
    ImageDraw.Draw(sh).rectangle([9, 9, pw + 9, ph + 9], fill=90)
    sh = sh.filter(ImageFilter.GaussianBlur(9))
    scene.paste(Image.new("L", sh.size, max(desk - 45, 0)), (ox - 9, oy - 4), sh)
    scene.paste(img, (ox, oy))

    scene = scene.rotate(rng.uniform(-angle, angle), resample=Image.BICUBIC, fillcolor=desk)
    scene = perspective(scene, persp, rng)
    scene = lighting(scene, light, rng)
    if blur > 0:
        scene = scene.filter(ImageFilter.GaussianBlur(blur))
    a = np.asarray(scene).astype(np.float32) + nprng.normal(0, noise, (scene.size[1], scene.size[0]))
    scene = Image.fromarray(np.clip(a, 0, 255).astype(np.uint8)).convert("RGB")
    s = rng.uniform(0.85, 1.0)
    return scene.resize((int(scene.size[0] * s), int(scene.size[1] * s)), Image.LANCZOS), jpeg


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True, help="directory to write images and truth.json into")
    ap.add_argument("--count", type=int, default=12, help="how many receipts to generate")
    ap.add_argument("--seed", type=int, default=1, help="a different seed is a different corpus")
    ap.add_argument("--tier", choices=[*TIERS, "mixed"], default="mixed", help="degradation level")
    ap.add_argument("--keep", action="store_true", help="merge into an existing truth.json instead of replacing it")
    args = ap.parse_args()

    mono = find_font("DejaVuSansMono.ttf", "Menlo.ttc", "Courier New.ttf")
    mono_b = find_font("DejaVuSansMono-Bold.ttf", "Menlo.ttc", "Courier New Bold.ttf")
    os.makedirs(args.out, exist_ok=True)

    truth = {}
    path = os.path.join(args.out, "truth.json")
    if args.keep and os.path.exists(path):
        with open(path) as fh:
            truth = json.load(fh)

    order = ["clean", "typical", "rough"]
    for i in range(args.count):
        rng = random.Random(args.seed * 100003 + i)
        tier = args.tier if args.tier != "mixed" else order[i % 3]
        lines, t, note = compose(rng, i)
        photo, q = photograph(render_receipt(lines, mono, mono_b), tier, seed=args.seed * 7919 + i)
        fn = f"IMG_{args.seed:03d}{i:03d}_{tier}.jpg"
        photo.save(os.path.join(args.out, fn), "JPEG", quality=q, subsampling=2)
        t["note"] = note
        truth[fn] = t
        print(f"  {fn:26} {tier:8} {photo.size[0]}x{photo.size[1]:<5} {note}")

    with open(path, "w") as fh:
        json.dump(truth, fh, indent=2, sort_keys=True)
        fh.write("\n")
    print(f"\nwrote {args.count} image(s) and {path} ({len(truth)} entries)")


if __name__ == "__main__":
    main()
