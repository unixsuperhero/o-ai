#!/usr/bin/env python3
"""Write o-ai's logo sets.

assets/            icon.svg, logo-light.svg, logo-dark.svg: concept o-ai-3,
                   "ai" in the family's rounded stroke with a spark as the
                   i's dot. The README shows this set.
docs/logo/alt/     the same three files for concept o-ai-5, Unplugged: the
                   plug stays out of the socket, because o-ai reads config
                   and caches and never makes a model request. Kept beside
                   the primary until the owner chooses.

Both concepts come from ~/hub/o-plans/icons/build.py, whose drawing this
copies. The sets follow ochazuke's and matomeru's assets/: a 128x128 icon on
a rounded tile (rx 28) in a dark tint of the tool's hue, and a logo with the
name in the family's monoline lettering, drawn as paths so no font is
needed. Standard library only, except --preview.

Regenerate from the repository root:

    python3 docs/logo/build.py                 # writes both sets
    python3 docs/logo/build.py --check         # the glyphs still redraw ochazuke's and matomeru's wordmarks
    python3 docs/logo/build.py --preview DIR   # also renders 16, 32 and 128 px PNGs and contact
                                               # sheets, DIR/ and DIR/alt/ (needs rsvg-convert, magick)
"""
import pathlib

import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
ASSETS = ROOT / "assets"
ALT = ROOT / "docs/logo/alt"
NAME = "o-ai"

# Indigo (ai, 藍). TILE is the icon's ground and the light logo's lettering;
# PALE is the dark logo's lettering.
TILE, DEEP, PRI, LIGHT, PALE = "#121a33", "#2b47b8", "#4a72f0", "#9db6ff", "#e3e9ff"
R = 'stroke-linecap="round" stroke-linejoin="round"'


def spark(cx, cy, s, fill):
    """A four-pointed spark of half-width s."""
    return (f'<path d="M{cx:g} {cy-s:g}Q{cx:g} {cy:g} {cx+s:g} {cy:g}Q{cx:g} {cy:g} {cx:g} {cy+s:g}'
            f'Q{cx:g} {cy:g} {cx-s:g} {cy:g}Q{cx:g} {cy:g} {cx:g} {cy-s:g}Z" fill="{fill}"/>')


# --- The family lettering ---------------------------------------------------
# The glyph table is o-proj's and o-inventory's (docs/logo/build.py in each),
# whose shapes are copied from the ochazuke and matomeru logos; d is o's bowl
# with h's ascender stem. Centre-line strokes 11 wide with round caps and
# joins, x-height y 44..84, ascenders from y 18, 15 between letters (centre
# line to centre line). Every family logo is 112 high (room for descenders),
# so the four READMEs' logos line up; README width = 360 * viewBox width / 518,
# ochazuke's letter height.
STROKE, GAP, HEIGHT = 11, 15, 112


def f(v):
    s = f"{v:.1f}"
    return s[:-2] if s.endswith(".0") else s


GLYPHS = {
    "o": (40, lambda x: f"M{f(x+20)} 44a20 20 0 1 0 0.01 0"),
    "c": (34.1, lambda x: f"M{f(x+34.1)} 49.9A20 20 0 1 0 {f(x+34.1)} 78.1"),
    "h": (36, lambda x: f"M{f(x)} 18V84M{f(x)} 62a18 18 0 0 1 36 0V84"),
    "a": (40, lambda x: f"M{f(x+20)} 44a20 20 0 1 0 0.01 0M{f(x+40)} 44V84"),
    "d": (40, lambda x: f"M{f(x+20)} 44a20 20 0 1 0 0.01 0M{f(x+40)} 18V84"),
    "z": (34, lambda x: f"M{f(x)} 44h34L{f(x)} 84h34"),
    "u": (36, lambda x: f"M{f(x)} 44V66a18 18 0 0 0 36 0M{f(x+36)} 44V84"),
    "k": (32, lambda x: f"M{f(x)} 18V84M{f(x+31)} 44L{f(x)} 70M{f(x+11)} 61L{f(x+32)} 84"),
    "e": (40, lambda x: f"M{f(x)} 64H{f(x+40)}A20 20 0 1 0 {f(x+34.1)} 78.1"),
    "m": (52, lambda x: f"M{f(x)} 44V84M{f(x)} 57a13 13 0 0 1 26 0V84M{f(x+26)} 57a13 13 0 0 1 26 0V84"),
    "t": (23, lambda x: f"M{f(x+7)} 26V73a11 11 0 0 0 11 11h4M{f(x)} 44H{f(x+23)}"),
    "r": (24, lambda x: f"M{f(x)} 44V84M{f(x)} 64a20 20 0 0 1 20 -20h4"),
    "i": (0, lambda x: f"M{f(x)} 44V84M{f(x)} 24h0.01"),
    "n": (36, lambda x: f"M{f(x)} 44V84M{f(x)} 62a18 18 0 0 1 36 0V84"),
    "v": (34, lambda x: f"M{f(x)} 44L{f(x+17)} 84L{f(x+34)} 44"),
    "-": (16, lambda x: f"M{f(x)} 64h16"),
    "p": (40, lambda x: f"M{f(x)} 44V106M{f(x+20)} 44a20 20 0 1 0 0.01 0"),
    "j": (0, lambda x: f"M{f(x)} 44V95a11 11 0 0 1 -11 11h-4M{f(x)} 24h0.01"),
    "y": (36, lambda x: f"M{f(x)} 44V66a18 18 0 0 0 36 0M{f(x+36)} 44V95a11 11 0 0 1 -11 11h-4"),
}


def wordmark(name, start=112):
    """Return (path data, x of each glyph, right edge of the last centre line)."""
    x, parts, xs = start, [], []
    for ch in name:
        w, d = GLYPHS[ch]
        parts.append(d(x))
        xs.append(x)
        x += w + GAP
    return "".join(parts), xs, x - GAP


def width_for(right):
    return int(right + STROKE / 2 + 3.5)  # ochazuke 509.1 -> 518, matomeru 524 -> 533


def word(d, ink):
    return (f'<path d="{d}" fill="none" stroke="{ink}" stroke-width="{STROKE}" '
            f'stroke-linecap="round" stroke-linejoin="round"/>\n')


def head(w, h, title):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" '
            f'role="img" aria-label="{title}">\n<title>{title}</title>\n')


def preview(src, out):
    """Render src's icon at 16, 32 and 128 px (and blown up 8x and 4x, to see
    the pixels) and both logos, into out (needs rsvg-convert and magick)."""
    import subprocess
    out.mkdir(parents=True, exist_ok=True)
    pngs = []
    for size in (16, 32, 128):
        p = out / f"icon-{size}.png"
        subprocess.run(["rsvg-convert", "-w", str(size), "-h", str(size), "-o", p, src / "icon.svg"], check=True)
        pngs.append(p)
    for theme, bg in (("light", "white"), ("dark", "#0d1117")):
        subprocess.run(["rsvg-convert", "-w", "720", "-b", bg, "-o", out / f"logo-{theme}.png",
                        src / f"logo-{theme}.svg"], check=True)
    big = []
    for size, k in ((16, 8), (32, 4)):
        b = out / f"icon-{size}-x{k}.png"
        subprocess.run(["magick", out / f"icon-{size}.png", "-filter", "point", "-resize", f"{k*100}%", b], check=True)
        big.append(b)
    subprocess.run(["magick", "-background", "#f6f3ee", *map(str, pngs), *map(str, big), "-bordercolor", "#f6f3ee",
                    "-border", "12", "-gravity", "center", "+append", out / "icons.png"], check=True)
    subprocess.run(["magick", out / "logo-light.png", out / "logo-dark.png", "-append", out / "logos.png"], check=True)
    print(out / "icons.png")
    print(out / "logos.png")


def check():
    """The glyph table must redraw ochazuke's and matomeru's wordmarks exactly."""
    import re
    ok = True
    for fam in ("ochazuke", "matomeru"):
        p = pathlib.Path.home() / f"proj/{fam}/assets/logo-light.svg"
        if not p.exists():
            print(f"skip {fam}: {p} not found")
            continue
        t = p.read_text()
        want = re.findall(r'<path d="(M[^"]*)" fill="none" stroke="#[0-9a-f]+" stroke-width="11"', t)[-1]
        want_w = int(re.search(r'viewBox="0 0 (\d+) 96"', t).group(1))
        d, _, right = wordmark(fam)
        split = lambda s: re.sub(r"([A-Za-z])", r" \1 ", s).split()
        a, b = split(d), split(want)
        near = lambda u, v: u == v or (u[0] not in "MmAaHhVvLlZz" and abs(float(u) - float(v)) < 0.15)
        same = len(a) == len(b) and all(map(near, a, b)) and width_for(right) == want_w
        ok &= same
        print(f"{fam}: {'same' if same else 'DIFFERENT'} wordmark ({width_for(right)} wide)")
    return ok


def icon(mark):
    return (head(128, 128, NAME)
            + f'<rect width="128" height="128" rx="28" fill="{TILE}"/>\n' + mark + "\n</svg>\n")


# --- o-ai-3: "ai" with a spark ----------------------------------------------

def ai_mark():
    return (f'<circle cx="49" cy="76" r="18" fill="none" stroke="{PALE}" stroke-width="12"/>\n'
            f'<path d="M67 58V94M91 58V94" stroke="{PALE}" stroke-width="12" {R}/>\n'
            + spark(91, 34, 14, PRI))


def ai_logo(theme):
    """The mark is lettering already, so the logo is the name alone with the
    icon's spark as the i's dot (10 above the stem, as in the icon)."""
    ink = TILE if theme == "light" else PALE
    d, xs, right = wordmark("o-a", 8)
    ix = right + GAP  # the i, drawn as its stem alone
    d += f"M{f(ix)} 44V84"
    return head(width_for(ix + 14), HEIGHT, NAME) + word(d, ink) + spark(ix, 20, 14, PRI) + "\n</svg>\n"


# --- o-ai-5: Unplugged -------------------------------------------------------
# Colours per ground: the icon's on dark; on white the socket takes the tile's
# navy and the plug's prongs the deep indigo so nothing pale vanishes.
PLUG = {
    "dark": dict(cable=LIGHT, plug=PRI, prong=PALE, socket=PALE, hole=TILE),
    "light": dict(cable="#7d97e6", plug="#3a5fe0", prong=DEEP, socket=TILE, hole="#ffffff"),
}


def plug_mark(theme):
    c = PLUG[theme]
    return (f'<path d="M14 98C30 98 26 64 38 64" fill="none" stroke="{c["cable"]}" stroke-width="8" {R}/>\n'
            f'<rect x="36" y="46" width="26" height="36" rx="7" fill="{c["plug"]}"/>\n'
            f'<path d="M62 55H74M62 73H74" stroke="{c["prong"]}" stroke-width="6" {R}/>\n'
            f'<rect x="84" y="36" width="28" height="56" rx="9" fill="{c["socket"]}"/>\n'
            f'<path d="M94 55H102M94 73H102" stroke="{c["hole"]}" stroke-width="6" {R}/>')


# The mark's drawn box in icon space is x 10..112, y 36..102 (measured from a
# 1280 px render). In the logo it is scaled to 84 wide, the width of ochazuke's
# and matomeru's marks, set 2 from the left, and lowered so the plug and
# socket (not the cable's tail) centre on the 96-high band.
PLUG_LOGO = "translate(-6.2 -4.6) scale(0.82)"
PLUG_RIGHT = 85.6


def plug_logo(theme):
    ink = TILE if theme == "light" else PALE
    d, _, right = wordmark(NAME, PLUG_RIGHT + 24 + STROKE / 2)
    return (head(width_for(right), HEIGHT, NAME)
            + f'<g transform="{PLUG_LOGO}">\n{plug_mark(theme)}\n</g>\n' + word(d, ink) + "</svg>\n")


def main():
    if sys.argv[1:] == ["--check"]:
        sys.exit(0 if check() else 1)
    sets = {
        ASSETS: {"icon.svg": icon(ai_mark()), "logo-light.svg": ai_logo("light"), "logo-dark.svg": ai_logo("dark")},
        ALT: {"icon.svg": icon(plug_mark("dark")), "logo-light.svg": plug_logo("light"),
              "logo-dark.svg": plug_logo("dark")},
    }
    for d, files in sets.items():
        d.mkdir(parents=True, exist_ok=True)
        for name, text in files.items():
            (d / name).write_text(text)
            print(d / name)
    if len(sys.argv) == 3 and sys.argv[1] == "--preview":
        out = pathlib.Path(sys.argv[2])
        preview(ASSETS, out)
        preview(ALT, out / "alt")


if __name__ == "__main__":
    main()
