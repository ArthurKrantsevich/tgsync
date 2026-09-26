"""Draws internal/brand/avatar.png, the bot's profile photo (640x640, safe inside a circle)."""
from PIL import Image, ImageDraw, ImageFilter

S = 2560  # drawn large, scaled down for smooth edges
OUT = 640


def lerp(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))


img = Image.new("RGB", (S, S))
top, bottom = (22, 30, 64), (18, 110, 130)  # deep navy → teal
px = img.load()
for y in range(S):
    for x in range(S):
        t = (x * 0.35 + y * 0.65) / S
        px[x, y] = lerp(top, bottom, min(1, max(0, t)))

# soft glow behind the glyph
glow = Image.new("L", (S, S), 0)
ImageDraw.Draw(glow).ellipse((S * 0.2, S * 0.22, S * 0.8, S * 0.82), fill=120)
glow = glow.filter(ImageFilter.GaussianBlur(S * 0.08))
img = Image.composite(Image.new("RGB", (S, S), (70, 200, 190)), img, glow.point(lambda v: v // 3))

d = ImageDraw.Draw(img)
white = (245, 248, 252)
w = int(S * 0.075)  # stroke width

# chevron ›
cx, cy = S * 0.36, S * 0.5
arm = S * 0.15
pts = [(cx - arm * 0.55, cy - arm), (cx + arm * 0.55, cy), (cx - arm * 0.55, cy + arm)]
d.line(pts, fill=white, width=w, joint="curve")
for p in (pts[0], pts[2]):
    d.ellipse((p[0] - w / 2, p[1] - w / 2, p[0] + w / 2, p[1] + w / 2), fill=white)

# cursor _
x0, y0 = S * 0.49, cy + arm - w / 2
d.rounded_rectangle((x0, y0, x0 + S * 0.2, y0 + w), radius=w / 2, fill=white)

# spark: the agent at work
sx, sy, r = S * 0.7, S * 0.33, S * 0.07
spark = [(sx, sy - r), (sx + r * 0.28, sy - r * 0.28), (sx + r, sy), (sx + r * 0.28, sy + r * 0.28),
         (sx, sy + r), (sx - r * 0.28, sy + r * 0.28), (sx - r, sy), (sx - r * 0.28, sy - r * 0.28)]
d.polygon(spark, fill=(255, 200, 110))
d.ellipse((sx + r * 0.9, sy + r * 1.1, sx + r * 1.3, sy + r * 1.5), fill=(255, 200, 110))

img.resize((OUT, OUT), Image.LANCZOS).save("internal/brand/avatar.png", optimize=True)
print("internal/brand/avatar.png written")
