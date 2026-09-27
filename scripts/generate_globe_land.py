#!/usr/bin/env python3
"""Build the globe's transparent land texture from Natural Earth 1:110m GeoJSON."""

import json
import sys
from pathlib import Path


def point(coordinates):
    longitude, latitude = coordinates
    return f"{(longitude + 180) * 1024 / 360:.2f},{(90 - latitude) * 512 / 180:.2f}"


def main(source, target):
    features = json.loads(Path(source).read_text())["features"]
    paths = []
    for feature in features:
        geometry = feature["geometry"]
        polygons = geometry["coordinates"] if geometry["type"] == "MultiPolygon" else [geometry["coordinates"]]
        rings = ["M" + " L".join(map(point, ring)) + " Z" for polygon in polygons for ring in polygon]
        paths.append(f'<path d="{" ".join(rings)}"/>')
    svg = (
        '<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="512" viewBox="0 0 1024 512">'
        '<g fill="#ffd45b" stroke="#f1b946" stroke-width="0.8" fill-rule="evenodd">'
        + "".join(paths)
        + '</g></svg>\n'
    )
    Path(target).write_text(svg)


if __name__ == "__main__":
    main(*sys.argv[1:])
