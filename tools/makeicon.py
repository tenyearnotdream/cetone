"""Turn the supplied artwork into a transparent-cornered icon set.

The source is an RGB image: a black rounded square on a pure white canvas, so
the four corners are white where the icon should show the desktop through. The
edge is anti-aliased (black blended onto white over about four pixels), so the
corners are removed by solving for coverage rather than by thresholding: a
threshold would leave a white ring or a jagged edge.
"""
from PIL import Image
from collections import deque
import os, sys

# Usage: python3 tools/makeicon.py
# Reads artwork/icon-source.png (the drawn artwork, an RGB square with white
# corners) and writes data/icon.png, the embedded icon and icons/hicolor/*.

SRC = os.environ.get(
    "CETONE_ICON_SOURCE",
    "artwork/icon-source.png",
)
OUT_MASTER = "data/icon.png"
OUT_EMBED = "internal/assets/icon.png"
OUT_SET = "icons/hicolor"

WHITE = 245          # min channel at or above which a border pixel is background
GRAY_SPREAD = 32     # max channel spread of an edge pixel (the edge is black/gray)

im = Image.open(SRC).convert("RGB")
w, h = im.size
px = im.load()

# 1. Flood fill the canvas from the border across near-white pixels. Only the
#    region connected to the outside is background; white highlights inside the
#    artwork are not reachable this way and stay opaque.
background = [[False] * w for _ in range(h)]
queue = deque()
def consider(x, y):
    if 0 <= x < w and 0 <= y < h and not background[y][x]:
        r, g, b = px[x, y]
        if min(r, g, b) >= WHITE:
            background[y][x] = True
            queue.append((x, y))
for x in range(w):
    consider(x, 0); consider(x, h - 1)
for y in range(h):
    consider(0, y); consider(w - 1, y)
while queue:
    x, y = queue.popleft()
    consider(x + 1, y); consider(x - 1, y); consider(x, y + 1); consider(x, y - 1)

# 2. Build the RGBA image. Background is transparent; the anti-aliased band just
#    inside it is turned into partial alpha by inverting the blend
#    (pixel = alpha*shape + (1-alpha)*white), with the shape's colour recovered.
out = Image.new("RGBA", (w, h))
opx = out.load()
band = set()
for y in range(h):
    for x in range(w):
        if background[y][x]:
            opx[x, y] = (0, 0, 0, 0)
            continue
        neighbours_background = (
            (x > 0 and background[y][x - 1]) or (x < w - 1 and background[y][x + 1]) or
            (y > 0 and background[y - 1][x]) or (y < h - 1 and background[y + 1][x])
        )
        r, g, b = px[x, y]
        if neighbours_background and max(r, g, b) - min(r, g, b) <= GRAY_SPREAD and max(r, g, b) < WHITE:
            band.add((x, y))

# Widen the band by one pixel so the far side of the slope is covered too.
grown = set()
for (x, y) in band:
    for dx in (-1, 0, 1):
        for dy in (-1, 0, 1):
            nx, ny = x + dx, y + dy
            if 0 <= nx < w and 0 <= ny < h and not background[ny][nx]:
                r, g, b = px[nx, ny]
                if max(r, g, b) - min(r, g, b) <= GRAY_SPREAD:
                    grown.add((nx, ny))
band |= grown

for y in range(h):
    for x in range(w):
        if background[y][x]:
            continue
        r, g, b = px[x, y]
        if (x, y) in band:
            # alphpa from coverage: the shape under the edge is black, so
            # coverage = 1 - measured/white.
            v = (r + g + b) / 3.0
            alpha = 1.0 - v / 255.0
            if alpha <= 0.0:
                opx[x, y] = (0, 0, 0, 0)
                continue
            if alpha >= 1.0:
                opx[x, y] = (r, g, b, 255)
                continue
            # Recover the shape's colour: measured = alpha*C + (1-alpha)*255.
            inv = 1.0 - alpha
            def unblend(c):
                return max(0, min(255, int(round((c - inv * 255.0) / alpha))))
            opx[x, y] = (unblend(r), unblend(g), unblend(b), int(round(alpha * 255)))
        else:
            opx[x, y] = (r, g, b, 255)

os.makedirs(os.path.dirname(OUT_MASTER), exist_ok=True)
out.save(OUT_MASTER)
print(f"master {w}x{h} -> {OUT_MASTER}")

# 3. The size set a Linux desktop picks from. Each size is resized from the
#    master through premultiplied alpha, so no dark fringe appears where the
#    edge becomes transparent.
def premultiplied_resize(image, size):
    canvas = Image.new("RGBA", image.size, (0, 0, 0, 0))
    canvas.alpha_composite(image)
    # PIL composites in straight alpha; resizing premultiplied data is what
    # avoids the fringe, so do it by hand.
    import struct
    src = image.convert("RGBA")
    spx = src.load()
    ww, hh = src.size
    pre = Image.new("RGBA", (ww, hh))
    ppx = pre.load()
    for y in range(hh):
        for x in range(ww):
            r, g, b, a = spx[x, y]
            ppx[x, y] = (r * a // 255, g * a // 255, b * a // 255, a)
    small = pre.resize((size, size), Image.LANCZOS)
    spx2 = small.load()
    result = Image.new("RGBA", (size, size))
    rpx = result.load()
    for y in range(size):
        for x in range(size):
            r, g, b, a = spx2[x, y]
            if a == 0:
                rpx[x, y] = (0, 0, 0, 0)
            else:
                rpx[x, y] = (min(255, r * 255 // a), min(255, g * 255 // a), min(255, b * 255 // a), a)
    return result

SIZES = [16, 22, 24, 32, 48, 64, 96, 128, 256, 512, 1024]
for size in SIZES:
    target = os.path.join(OUT_SET, f"{size}x{size}", "apps")
    os.makedirs(target, exist_ok=True)
    premultiplied_resize(out, size).save(os.path.join(target, "cetone.png"))
    print(f"  {size}x{size}")

# The embedded copy is drawn by the window and scaled down to the status area.
premultiplied_resize(out, 512).save(OUT_EMBED)
print(f"embedded -> {OUT_EMBED}")


# 4. Two variants of the same artwork.
#
# The drawing is a blue whale on a near-black rounded tile. On a dark taskbar or
# panel the tile merges with the bar and only the bright rim reads; on a pale one
# it is a heavy black block. So the same artwork ships twice: the tile as drawn,
# and the tile repainted pale.
#
# The repaint is safe because the artwork's own darks are blue (measured: every
# dark pixel inside the whale is saturated, none is neutral black), so
# "neutral and dark" is exactly the tile and nothing else.
def light_variant(image):
    w, h = image.size
    src = image.load()
    out = Image.new("RGBA", (w, h))
    px = out.load()
    for y in range(h):
        for x in range(w):
            r, g, b, a = src[x, y]
            if a == 0:
                continue
            mx, mn = max(r, g, b), min(r, g, b)
            neutral = (mx - mn) <= 24
            if neutral:
                # The tile: a pale sea, slightly deeper at the bottom, so the
                # whale still sits on water rather than on a flat sheet.
                t = y / max(h - 1, 1)
                top = (232, 245, 253)     # #E8F5FD
                bottom = (198, 228, 247)  # #C6E4F7
                px[x, y] = (
                    int(top[0] + (bottom[0] - top[0]) * t),
                    int(top[1] + (bottom[1] - top[1]) * t),
                    int(top[2] + (bottom[2] - top[2]) * t),
                    a,
                )
            else:
                px[x, y] = (r, g, b, a)
    return out

light = light_variant(out)
light.save("artwork/icon-light.png")
print("artwork/icon-light.png")

for size in SIZES:
    target = os.path.join(OUT_SET, f"{size}x{size}", "apps")
    os.makedirs(target, exist_ok=True)
    premultiplied_resize(light, size).save(os.path.join(target, "cetone-light.png"))
premultiplied_resize(light, 512).save("internal/assets/icon-light.png")
print("internal/assets/icon-light.png")
