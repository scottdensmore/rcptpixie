# genphotos

Generates photographed-looking receipts with known ground truth, for the
`eval`-tagged accuracy harness in `internal/analyze`.

The committed fixtures in `testdata/` are clean renders. They measure the text
path well and the vision path barely at all — a rasterized page is a crisp,
square, evenly lit image, which is nothing like a phone photo of a till roll.
This produces the latter without anyone handing over their real receipts.

## Use

```bash
pip install -r scripts/genphotos/requirements.txt

python3 scripts/genphotos/genphotos.py --out testdata/real --count 24 --seed 3
go test -tags eval ./internal/analyze -run TestRealCorpus -eval.corpus=testdata/real -v
```

`testdata/real/` is gitignored, so generated corpora and any real receipts you
put beside them stay local.

| flag | |
| --- | --- |
| `--count` | how many receipts |
| `--seed` | a different seed is a different corpus; the same seed reproduces one exactly |
| `--tier` | `clean`, `typical`, `rough`, or `mixed` (default) |
| `--keep` | merge into an existing `truth.json` rather than replacing it |

## What it simulates

Content is procedural — vendors, items, amounts, currencies and date formats are
drawn per receipt — so a new seed is a new corpus rather than the same receipts
again. Hotel folios with a check-in and check-out date appear about one time in
six. `truth.json` is written in the format the harness reads, with a `note`
recording the date exactly as printed.

The paper is then degraded the way a camera degrades it:

- perspective keystone from holding the phone off-axis, plus rotation
- a lighting gradient and a specular hot spot
- the curl of a receipt that will not lie flat
- uneven thermal fade, which real till rolls have down their length
- defocus, sensor noise, and JPEG compression
- the receipt sitting on a desk with a soft shadow, so the paper must be found

Three tiers set how hard each of those is pushed. `rough` is a bad photo — dim,
steeply angled, soft — but still legible to a person; that is the intent, since
an illegible image measures the generator rather than the tool.

## Reading the results

`note` marks a date as **ambiguous** when swapping day and month would still give
a valid, different date — `03/04/2024` is ambiguous, `04/22/2024` is not, and
`05.05.2023` is not because swapping changes nothing. That distinction is the
useful one: measured across two corpora, the dominant date failure is not a
misread digit but a day/month swap on an ambiguous format, and it happens on
sharp images as readily as on soft ones. Totals behave the other way round —
they fail only when the image is genuinely degraded.

## Honest limits

These are simulated photographs. They cover skew, lighting, fade, defocus, noise
and compression. They do not cover a creased or torn receipt, glare that blows
out a line completely, a curled roll that goes out of focus at one end, or a
handwritten tip. Treat a good score here as a floor, not as evidence the tool
handles a shoebox of real receipts.
