#!/usr/bin/env python3
"""Generate robot-head icons for the browser extension toolbar.

off (grey)  = disconnected / unconfigured / error
on  (yellow) = connected

Outputs PNGs at 16/32/48/128 px into ../ (the icons/ directory).
"""
import os
from PIL import Image, ImageDraw

# ── Tunable geometry (all fractions of canvas size) ─────────────
HEAD_W          = 0.65   # head width
HEAD_ASPECT     = 0.58 / 0.63  # head height / width
HEAD_BOTTOM     = 0.90   # head bottom edge position
HEAD_RADIUS     = 0.14   # corner radius

ANT_BALL_R      = 0.08   # antenna ball radius
ANT_LINE_W      = 0.07   # antenna rod width

EYE_R           = 0.10   # eye radius
EYE_Y_FRAC      = 0.35   # eye vertical position within head
EYE_DX_FRAC     = 0.25   # eye horizontal offset within head width
PUPIL_R_FRAC    = 0.50   # pupil radius as fraction of eye radius

MOUTH_W_FRAC    = 0.32   # mouth width as fraction of head width
MOUTH_H         = 0.045  # mouth height

OUTDIR = os.path.join(os.path.dirname(os.path.dirname(__file__)), "icons")

PALETTES = {
    "off": {
        "head":  (154, 160, 166),
        "eye":   (255, 255, 255),
        "pupil": (154, 160, 166),
        "ant":   (154, 160, 166),
        "ant_b": (154, 160, 166),
        "mouth": (154, 160, 166),
        "bg":    (0, 0, 0, 0),
    },
    "on": {
        "head":  (255, 193, 7),    # amber yellow
        "eye":   (255, 255, 255),
        "pupil": (255, 193, 7),
        "ant":   (255, 193, 7),
        "ant_b": (234, 67, 53),     # red antenna tip
        "mouth": (255, 255, 255),
        "bg":    (0, 0, 0, 0),
    },
}

SIZES = [16, 32, 48, 128]


def draw_robot(size, pal):
    img = Image.new("RGBA", (size, size), pal["bg"])
    d = ImageDraw.Draw(img)
    s = size

    def c(name):
        return pal[name][:3]

    head_w = s * HEAD_W
    head_h = head_w * HEAD_ASPECT
    head_x = (s - head_w) / 2
    head_y = s * HEAD_BOTTOM - head_h
    d.rounded_rectangle(
        [head_x, head_y, head_x + head_w, head_y + head_h],
        radius=s * HEAD_RADIUS,
        fill=c("head"),
    )

    # Antenna ball flush to top, rod connects it to head.
    ant_x = s / 2
    ant_ball_r = s * ANT_BALL_R
    ant_ball_cy = ant_ball_r
    d.line(
        [(ant_x, head_y), (ant_x, ant_ball_cy)],
        fill=c("ant"),
        width=max(1, int(s * ANT_LINE_W)),
    )
    d.ellipse(
        [ant_x - ant_ball_r, ant_ball_cy - ant_ball_r,
         ant_x + ant_ball_r, ant_ball_cy + ant_ball_r],
        fill=c("ant_b"),
    )

    eye_r = s * EYE_R
    eye_y = head_y + head_h * EYE_Y_FRAC
    eye_dx = head_w * EYE_DX_FRAC
    for cx in (head_x + eye_dx, head_x + head_w - eye_dx):
        d.ellipse(
            [cx - eye_r, eye_y - eye_r, cx + eye_r, eye_y + eye_r],
            fill=c("eye"),
        )
        pr = eye_r * PUPIL_R_FRAC
        d.ellipse(
            [cx - pr, eye_y - pr, cx + pr, eye_y + pr],
            fill=c("pupil"),
        )

    mouth_w = head_w * MOUTH_W_FRAC
    mouth_h = max(1, s * MOUTH_H)
    mouth_x = (s - mouth_w) / 2
    mouth_y = head_y + head_h * 0.72
    d.rounded_rectangle(
        [mouth_x, mouth_y, mouth_x + mouth_w, mouth_y + mouth_h],
        radius=mouth_h / 2,
        fill=c("mouth"),
    )

    return img


def main():
    os.makedirs(OUTDIR, exist_ok=True)
    for scheme, pal in PALETTES.items():
        for sz in SIZES:
            img = draw_robot(sz, pal)
            fname = f"robot-{scheme}-{sz}.png"
            img.save(os.path.join(OUTDIR, fname))
            print(f"wrote {fname} ({sz}×{sz})")


if __name__ == "__main__":
    main()
