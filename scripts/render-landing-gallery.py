"""Convert the real terminal layout test's true-color ANSI captures to SVG.

Usage: python3 scripts/render-landing-gallery.py /tmp/bodek-landing-previews
No engine, provider credentials, or image-generation service is involved.
"""
import html
import re
import sys
import unicodedata
from pathlib import Path

THEMES = {'ember-dark': ('#101114', '#E7E9EE'), 'ember-light': ('#FAF8F2', '#22252C'),
          'high-contrast': ('#000000', '#FFFFFF'), 'classic': ('#111118', '#E5E7EB')}
ESC = re.compile(r'\x1b\[([0-9;]*)m')

def cells(text):
    return sum(0 if unicodedata.combining(c) or c in '\ufe0f\u200d' else
               2 if unicodedata.east_asian_width(c) in 'WF' else 1 for c in text)

def render(source, background, foreground):
    lines = source.splitlines()
    width, height = 1240, len(lines) * 22 + 40
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
           '<title>Bodek terminal layout — synthetic checkout review session</title>',
           f'<rect width="100%" height="100%" fill="{background}"/>']
    for row, line in enumerate(lines):
        fg, bg, bold, column = foreground, None, False, 0
        parts = ESC.split(line)
        for i, part in enumerate(parts):
            if i % 2:
                codes = [int(n) if n else 0 for n in part.split(';')]
                j = 0
                while j < len(codes):
                    code = codes[j]
                    if code == 0: fg, bg, bold = foreground, None, False
                    elif code == 1: bold = True
                    elif code == 22: bold = False
                    elif code == 39: fg = foreground
                    elif code == 49: bg = None
                    elif code in (38, 48) and codes[j + 1:j + 2] == [2]:
                        color = '#%02x%02x%02x' % tuple(codes[j + 2:j + 5])
                        if code == 38: fg = color
                        else: bg = color
                        j += 4
                    j += 1
                continue
            if '\x1b' in part: raise ValueError('Unsupported terminal control sequence')
            count = cells(part)
            x, y = 20 + column * 10, 20 + row * 22
            if bg and count: out.append(f'<rect x="{x}" y="{y}" width="{count * 10}" height="22" fill="{bg}"/>')
            if part.strip():
                out.append(f'<text x="{x}" y="{y + 16}" fill="{fg}" font-family="monospace" font-size="16" font-weight="{700 if bold else 400}" xml:space="preserve" textLength="{count * 10}" lengthAdjust="spacingAndGlyphs">{html.escape(part)}</text>')
            column += count
    return '\n'.join(out + ['</svg>'])

if __name__ == '__main__':
    target = Path(__file__).resolve().parents[1] / 'docs/gallery'
    target.mkdir(exist_ok=True)
    for name, (bg, fg) in THEMES.items():
        source = (Path(sys.argv[1]) / f'{name}-120-true.ansi').read_text()
        (target / f'{name}.svg').write_text(render(source, bg, fg))
