#!/usr/bin/env python3
"""Rebuild the approved logo from geometric SVG primitives and compare rasters.

Requires Pillow, NumPy, and rsvg-convert. Run from the repository root:
    python3 scripts/rebuild-logo.py --label iteration-1

All lettering is outlined. No fonts or embedded raster images are used in SVG.
"""

from __future__ import annotations

import argparse
import json
import math
from pathlib import Path
import subprocess
import xml.etree.ElementTree as ET

import numpy as np
from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[1]
BRAND = ROOT / "assets/brand"
QA = BRAND / "qa"
STEM = "webdevicecable-logo-globe-terminal-cable"
REFERENCE = BRAND / f"{STEM}.png"
SVG = BRAND / f"{STEM}.svg"
PNG = BRAND / f"{STEM}-vector.png"
INK = (16, 18, 19)

# Integer/half-unit design coordinates, rather than a raster auto-trace.
P = {
    "cx": 512.0, "cy": 430.0, "radius": 313.5, "ring": 46.0,
    "ring_start": 50.0, "ring_end": 25.0,
    "meridian_rx": 182.25, "meridian_ry": 291.5, "grid": 33.0,
    "meridian_stroke": 34.0, "latitude_stroke": 29.25,
    "latitude_cx": 391.75, "latitude_y": 243.0,
    "latitude_rx": 202.0, "latitude_ry": 67.0,
    "chevron_x": 591.5, "chevron_tip": 665.75,
    "chevron_y": 426.0, "chevron_half_height": 70.0, "chevron_stroke": 44.25,
    "underscore_x1": 679.0, "underscore_x2": 751.5,
    "underscore_y": 498.0, "underscore_stroke": 38.25,
    "plug_angle": 44.75, "plug_width": 65.25,
    "plug_shift_x": -1.5, "plug_shift_y": -5.5,
    "plug_length": 69.25, "tongue_length": 35.5,
    "tongue_width": 39.5,
    "type_shift_x": 0.0, "type_shift_y": 0.0,
    "c_cx": 745.5, "c_cy": 870.75,
    "c_rx": 97.5, "c_ry": 84.25,
    "c_inner_rx": 46.5, "c_inner_ry": 41.75,
    "c_opening": 23.0,
}


def f(x: float) -> str:
    return f"{x:.4f}".rstrip("0").rstrip(".")


def point(cx: float, cy: float, rx: float, ry: float, degrees: float):
    a = math.radians(degrees)
    return np.array([cx + rx * math.cos(a), cy + ry * math.sin(a)])


def xy(p) -> str:
    return f"{f(float(p[0]))} {f(float(p[1]))}"


def rounded_polygon(points, radii):
    """Fillet a polygon with true circular arcs, preserving straight stems."""
    vertices = [np.array(p, dtype=float) for p in points]
    corners = []
    for i, v in enumerate(vertices):
        prev, nxt = vertices[i - 1], vertices[(i + 1) % len(vertices)]
        incoming, outgoing = prev - v, nxt - v
        l1, l2 = np.linalg.norm(incoming), np.linalg.norm(outgoing)
        u, w = incoming / l1, outgoing / l2
        angle = math.acos(float(np.clip(np.dot(u, w), -1, 1)))
        tangent = radii[i] / math.tan(angle / 2) if radii[i] else 0
        tangent = min(tangent, l1 * .45, l2 * .45)
        radius = tangent * math.tan(angle / 2)
        cross = float((-u[0]) * w[1] - (-u[1]) * w[0])
        corners.append((v + u * tangent, v + w * tangent, radius, int(cross > 0)))
    d = [f"M {xy(corners[-1][1])}"]
    for start, end, radius, sweep in corners:
        d.append(f"L {xy(start)}")
        if radius:
            d.append(f"A {f(radius)} {f(radius)} 0 0 {sweep} {xy(end)}")
    return " ".join(d) + " Z"


def c_outline(p):
    """Elliptical C, with symmetric openings and softly rounded end cuts."""
    cx, cy = p["c_cx"], p["c_cy"]
    rx, ry = p["c_rx"], p["c_ry"]
    ix, iy, a = p["c_inner_rx"], p["c_inner_ry"], p["c_opening"]
    delta_o, delta_i = 7.0, 12.0
    ou = point(cx, cy, rx, ry, -a)
    ol = point(cx, cy, rx, ry, a)
    iu = point(cx, cy, ix, iy, -a)
    il = point(cx, cy, ix, iy, a)
    ous = point(cx, cy, rx, ry, -a-delta_o)
    ols = point(cx, cy, rx, ry, a+delta_o)
    ius = point(cx, cy, ix, iy, -a-delta_i)
    ils = point(cx, cy, ix, iy, a+delta_i)
    lerp = lambda u, v, t: u + (v-u)*t
    return " ".join([
        f"M {xy(ous)} A {f(rx)} {f(ry)} 0 1 0 {xy(ols)}",
        f"Q {xy(ol)} {xy(lerp(ol, il, .2))}",
        f"L {xy(lerp(ol, il, .8))} Q {xy(il)} {xy(ils)}",
        f"A {f(ix)} {f(iy)} 0 1 1 {xy(ius)}",
        f"Q {xy(iu)} {xy(lerp(iu, ou, .2))}",
        f"L {xy(lerp(iu, ou, .8))} Q {xy(ou)} {xy(ous)} Z",
    ])


def build_svg(p):
    cx, cy, r = p["cx"], p["cy"], p["radius"]
    first = point(cx, cy, r, r, p["ring_start"])
    last = point(cx, cy, r, r, p["ring_end"])
    anchor = point(cx, cy, r, r, p["plug_angle"])
    anchor += np.array([p["plug_shift_x"], p["plug_shift_y"]])
    w_points = [(192,792),(242,792),(267,874),(301,792),
                (345,792),(379,874),(404,792),(454,792),
                (400,950),(356,950),(323,865),(290,950),(246,950)]
    w_radii = [8,8,0,8,8,0,8,8,10,10,0,10,10]
    w = rounded_polygon(w_points, w_radii)
    d = ("M 470 792 H 552 A 89 79 0 0 1 552 950 H 470 "
         "A 8 8 0 0 1 462 942 V 800 A 8 8 0 0 1 470 792 Z "
         "M 508 833 H 544 A 42 38 0 0 1 544 909 H 508 Z")
    desc = ("A circular cable encloses a globe hemisphere on the left and a "
            "terminal prompt on the right, with WDC underneath.")
    return f'''<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024" fill="none" role="img" aria-labelledby="logo-title logo-description">
  <title id="logo-title">WebDeviceCable</title>
  <desc id="logo-description">{desc}</desc>
  <defs>
    <clipPath id="globe-disk"><circle cx="{f(cx)}" cy="{f(cy)}" r="{f(r)}"/></clipPath>
    <clipPath id="globe-left"><rect x="{f(cx-r)}" y="{f(cy-r)}" width="{f(r)}" height="{f(2*r)}"/></clipPath>
  </defs>
  <g id="symbol" stroke="#101213" stroke-linecap="round" stroke-linejoin="round">
    <g clip-path="url(#globe-disk)">
      <g id="globe-grid" clip-path="url(#globe-left)" stroke-width="{f(p['grid'])}">
        <path d="M {f(cx)} {f(cy-p['meridian_ry'])} A {f(p['meridian_rx'])} {f(p['meridian_ry'])} 0 0 0 {f(cx)} {f(cy+p['meridian_ry'])}" stroke-width="{f(p['meridian_stroke'])}"/>
        <path d="M {f(cx-r)} {f(cy)} H {f(cx)}"/>
        <ellipse cx="{f(p['latitude_cx'])}" cy="{f(p['latitude_y'])}" rx="{f(p['latitude_rx'])}" ry="{f(p['latitude_ry'])}" stroke-width="{f(p['latitude_stroke'])}" clip-path="url(#upper-latitude)"/>
        <ellipse cx="{f(p['latitude_cx'])}" cy="{f(2*cy-p['latitude_y'])}" rx="{f(p['latitude_rx'])}" ry="{f(p['latitude_ry'])}" stroke-width="{f(p['latitude_stroke'])}" clip-path="url(#lower-latitude)"/>
      </g>
    </g>
    <path id="hemisphere-divider" d="M {f(cx)} {f(cy-r)} V {f(cy+r)}" stroke-width="{f(p['ring'])}"/>
    <path id="circular-cable" d="M {xy(first)} A {f(r)} {f(r)} 0 1 1 {xy(last)}" stroke-width="{f(p['ring'])}"/>
    <path id="terminal-chevron" d="M {f(p['chevron_x'])} {f(p['chevron_y']-p['chevron_half_height'])} L {f(p['chevron_tip'])} {f(p['chevron_y'])} L {f(p['chevron_x'])} {f(p['chevron_y']+p['chevron_half_height'])}" stroke-width="{f(p['chevron_stroke'])}"/>
    <path id="terminal-cursor" d="M {f(p['underscore_x1'])} {f(p['underscore_y'])} H {f(p['underscore_x2'])}" stroke-width="{f(p['underscore_stroke'])}"/>
    <g id="cable-plug" transform="translate({xy(anchor)}) rotate({f(p['plug_angle']-90)})" fill="#101213" stroke="none">
      <rect x="-{f(p['plug_length']/2)}" y="-{f(p['plug_width']/2)}" width="{f(p['plug_length'])}" height="{f(p['plug_width'])}" rx="9"/>
      <rect x="{f(p['plug_length']/2-2)}" y="-{f(p['tongue_width']/2)}" width="{f(p['tongue_length']+2)}" height="{f(p['tongue_width'])}" rx="4"/>
    </g>
  </g>
  <g id="wordmark" fill="#101213" transform="translate({f(p['type_shift_x'])} {f(p['type_shift_y'])})">
    <path id="letter-w" d="{w}"/>
    <path id="letter-d" fill-rule="evenodd" d="{d}"/>
    <path id="letter-c" d="{c_outline(p)}"/>
  </g>
</svg>
'''.replace('  </defs>', f'''    <clipPath id="upper-latitude"><rect x="0" y="{f(p['latitude_y'])}" width="1024" height="512"/></clipPath>
    <clipPath id="lower-latitude"><rect x="0" y="0" width="1024" height="{f(2*cy-p['latitude_y'])}"/></clipPath>
  </defs>''')


def metrics(reference, rendered):
    a, b = reference > .5, rendered > .5
    union = np.count_nonzero(a | b)
    intersection = np.count_nonzero(a & b)
    return {
        "foreground_iou": round(intersection / union, 6),
        "dice": round(2 * intersection / (a.sum()+b.sum()), 6),
        "different_mask_pixels": int(np.count_nonzero(a ^ b)),
        "mean_absolute_coverage_error": round(float(np.mean(np.abs(reference-rendered))), 6),
    }


def compare(label):
    original = Image.open(REFERENCE).convert("RGBA")
    w, h = original.size
    subprocess.run(["rsvg-convert", "-w", str(w), "-h", str(h), "-o", str(PNG), str(SVG)], check=True)
    rendered = Image.open(PNG).convert("RGBA")
    # Compare coverage, avoiding the generated source's nonuniform RGB noise.
    a = np.asarray(original)[:, :, 3].astype(float) / 255
    b = np.asarray(rendered)[:, :, 3].astype(float) / 255
    regions = {
        "whole_logo": (0, 0, w, h),
        "symbol": (180, 90, 1060, 950),
        "globe_half": (190, 90, 662, 950),
        "terminal_half": (660, 300, 960, 650),
        "cable_plug": (820, 660, 1010, 865),
        "wordmark": (210, 950, 1040, 1180),
    }
    report = {"reference": REFERENCE.name, "svg": SVG.name, "resolution": [w, h],
              "pixels_compared": w*h,
              "method": "All source and SVG pixels compared by alpha coverage; foreground masks thresholded at 0.5. RGB noise is excluded. Background area is not used to inflate the foreground IoU.",
              "regions": {}, "geometry": P,
              "intentional_corrections": [
                  "A true circular SVG arc replaces the slightly irregular generated ring; centered at x=512 in a 1024-unit viewBox.",
                  "Latitude ellipses are exact mirror images around the equator, and the meridian is an exact ellipse.",
                  "The W is mirror-symmetric. W and D share a cap line and baseline; C uses a conventional optical overshoot.",
                  "The plug tongue is attached to its body. One visible mating gap is retained at the cable end, rather than reproducing two disconnected pieces or closing the gap to optimize pixel overlap.",
                  "All fills and strokes are uniformly opaque #101213; generated color/alpha noise and stray faint pixels are discarded.",
              ],
              "geometry_checks": validate_svg(SVG),
              }
    for key, (x0,y0,x1,y1) in regions.items():
        report["regions"][key] = metrics(a[y0:y1,x0:x1], b[y0:y1,x0:x1])
    QA.mkdir(parents=True, exist_ok=True)
    history_path = QA / "logo-comparison-history.json"
    history = json.loads(history_path.read_text()) if history_path.exists() else []
    history.append({"label": label, **report})
    history_path.write_text(json.dumps(history, indent=2) + "\n")
    (QA / "logo-comparison-metrics.json").write_text(json.dumps(report, indent=2) + "\n")
    # Red = only in PNG; blue = only in SVG; black = agreement.
    am, bm = a>.5, b>.5
    diff = np.full((h,w,3), 255, dtype=np.uint8)
    diff[am & bm] = INK
    diff[am & ~bm] = (224, 55, 72)
    diff[bm & ~am] = (35, 112, 232)
    diff_image = Image.fromarray(diff)
    diff_image.save(QA / "logo-pixel-difference.png")
    white = Image.new("RGBA", (w,h), "white")
    source_white = Image.alpha_composite(white, original).convert("RGB")
    vector_white = Image.alpha_composite(white, rendered).convert("RGB")
    board = Image.new("RGB", (1800, 730), "#eef1f4")
    draw = ImageDraw.Draw(board)
    font = ImageFont.load_default(size=22)
    small = ImageFont.load_default(size=17)
    images = [source_white, vector_white, diff_image]
    titles = ["Generated PNG", "Geometric SVG", "Pixel difference"]
    for i,(im,title) in enumerate(zip(images,titles)):
        x = 20+i*600
        draw.text((x+12,16),title,fill=INK,font=font)
        board.paste(im.resize((560,560), Image.Resampling.LANCZOS), (x+10,55))
    draw.text((1230,625),"Red: PNG only  |  Blue: SVG only",fill=INK,font=small)
    iou=report['regions']['whole_logo']['foreground_iou']
    draw.text((32,650),f"Foreground overlap (IoU): {iou:.2%}. Exact circle and ellipse geometry; lettering is outlined.",fill=INK,font=font)
    board.save(QA / "logo-comparison.png")
    print(json.dumps({key:value for key,value in report['regions'].items()},indent=2))


def validate_svg(path):
    root = ET.parse(path).getroot()
    ns = {"s": "http://www.w3.org/2000/svg"}
    arc = root.find(".//s:path[@id='circular-cable']", ns)
    parts = arc.attrib["d"].split()
    k = parts.index("A")
    assert float(parts[k+1]) == float(parts[k+2]) == P["radius"]
    assert root.attrib["viewBox"] == "0 0 1024 1024"
    assert not root.findall(".//s:image", ns)
    assert not root.findall(".//s:text", ns)
    assert P["cx"] == 512
    latitudes = root.findall(".//s:ellipse", ns)
    assert len(latitudes) == 2
    assert float(latitudes[0].attrib["cy"])+float(latitudes[1].attrib["cy"]) == 2*P["cy"]
    assert latitudes[0].attrib["rx"] == latitudes[1].attrib["rx"]
    assert latitudes[0].attrib["ry"] == latitudes[1].attrib["ry"]
    # Conservative gap measurement: distance from the cable's round end cap
    # to the bounding rectangle of the plug tongue, in the plug's local frame.
    endpoint = point(P["cx"], P["cy"], P["radius"], P["radius"], P["ring_end"])
    anchor = point(P["cx"], P["cy"], P["radius"], P["radius"], P["plug_angle"])
    anchor += [P["plug_shift_x"], P["plug_shift_y"]]
    angle = math.radians(P["plug_angle"]-90)
    rotate = np.array([[math.cos(angle),math.sin(angle)],[-math.sin(angle),math.cos(angle)]])
    q = rotate @ (endpoint-anchor)
    lo = np.array([P["plug_length"]/2-2,-P["tongue_width"]/2])
    hi = np.array([P["plug_length"]/2+P["tongue_length"],P["tongue_width"]/2])
    distance = np.linalg.norm(np.maximum(np.maximum(lo-q,q-hi),0))
    gap = float(distance-P["ring"]/2)
    assert gap > 4, "The plug must not close the cable's visible mating gap."
    return {"circular_arc_has_equal_x_y_radii": True,
            "symbol_centered_horizontally": True,
            "latitude_arcs_mirrored": True,
            "lettering_outlined_no_font_dependency": True,
            "no_embedded_raster": True,
            "transparent_background": True,
            "circle_center": [P["cx"], P["cy"]],
            "cable_centerline_radius": P["radius"],
            "outer_radius": P["radius"]+P["ring"]/2,
            "inner_radius": P["radius"]-P["ring"]/2}


def make_symbol_and_scale_check():
    ns = "http://www.w3.org/2000/svg"
    ET.register_namespace("", ns)
    root = ET.parse(SVG).getroot()
    wordmark = root.find(f"{{{ns}}}g[@id='wordmark']")
    root.remove(wordmark)
    root.set("viewBox", "144 62 736 736")
    root.find(f"{{{ns}}}title").text = "WebDeviceCable symbol"
    root.find(f"{{{ns}}}desc").text = "A circular cable encloses a globe hemisphere and a terminal prompt."
    symbol_svg = BRAND / f"{STEM}-symbol.svg"
    ET.ElementTree(root).write(symbol_svg, encoding="unicode", xml_declaration=False)
    sizes = [16, 24, 32, 48, 64, 128]
    sheet = Image.new("RGB", (1100, 460), "#eef1f4")
    draw = ImageDraw.Draw(sheet)
    font = ImageFont.load_default(size=19)
    for row,(name,src) in enumerate([("Complete logo", SVG),("Symbol only", symbol_svg)]):
        y = 25+row*210
        draw.text((25,y),name,fill=INK,font=font)
        for col,size in enumerate(sizes):
            x = 55+col*175
            out = QA / f".{name.replace(' ','-')}-{size}.png"
            subprocess.run(["rsvg-convert","-w",str(size),"-h",str(size),"-o",str(out),str(src)],check=True)
            im = Image.open(out).convert("RGBA")
            draw.rounded_rectangle((x-15,y+35,x+140,y+185),radius=8,fill="white")
            sheet.paste(im,(x+(128-size)//2,y+45+(128-size)//2),im)
            draw.text((x+38,y+188),f"{size}px",fill=INK,font=font)
            out.unlink()
    sheet.save(QA / "logo-scale-check.png")


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--label",default="comparison")
    parser.add_argument("--parameters",type=Path)
    args=parser.parse_args()
    if args.parameters:
        P.update(json.loads(args.parameters.read_text()))
    SVG.write_text(build_svg(P))
    compare(args.label)
    make_symbol_and_scale_check()


if __name__ == "__main__":
    main()
