"""Rebuild the logo kit. Requires Python 3 and Pillow: python -m pip install pillow."""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent
MASTER = [
    '.............###.................',
    '............#####................',
    '............#####................',
    '.......###..#####................',
    '......#####..####................',
    '......#####..#####...............',
    '...##..#####..####...............',
    '..###..#####..####...............',
    '..####..####..####...............',
    '...####..####..###...............',
    '....####..###..###...............',
    '.....####..###..##...............',
    '.##...####..##..##...............',
    '####...###..###..#...............',
    '#####...###..##..#...............',
    '.#####...###..##.#.....#####.....',
    '..#####...###..#.#....#######.....',
    '...#####...##..#.#...#########....',
    '....#####...##..#....#####.###....',
    '.....#####...#..#...#############',
    '......#####...#.#..#############.',
    '.......#####...################..',
    '........#####..###############...',
    '.........#####################...',
    '..........###################....',
    '...........##################....',
    '............################.....',
    '..............#############......',
    '...............###########.......',
    '................#########........',
    '.................##...##.........',
    '..................#....#.........',
    '..................#....#.........',
    '.................###...###.......',
]
# An intentionally simpler drawing for 16 px / compact terminal use.
MICRO = [
    '......##........',
    '.....####.......',
    '...##.###.......',
    '..###..##.......',
    '.##.##.##.......',
    '.###.##.#.......',
    '..###.#.#.......',
    '##.###.#...###..',
    '###.##.#..##.##.',
    '.###.##..#######',
    '..###..########.',
    '...############.',
    '....##########..',
    '.....########...',
    '.......##.##....',
    '.......##.##....',
]

# Scaled down from MASTER and retouched (the eye) for the 5-line braille logo.
BRAILLE = [
    '.......###..........',
    '.......###..........',
    '....##.###..........',
    '..#.###.###.........',
    '.##.###.###.........',
    '..##.###.##.........',
    '...##.##.##.........',
    '##..##.##.#.........',
    '###..#.##.#.........',
    '.###..#...#..###....',
    '..###..#.#..##.##...',
    '...###...#..#######.',
    '....###..##########.',
    '.....#############..',
    '......###########...',
    '.......##########...',
    '.........#######....',
    '..........#####.....',
    '..........#..#......',
    '..........##.##.....',
]

def canvas(rows, size, ox=0, oy=0):
    assert len(rows) + oy <= size
    assert all(set(r) <= {'.', '#'} and len(r) + ox <= size for r in rows)
    pixels = [[False]*size for _ in range(size)]
    for y,row in enumerate(rows):
        for x,char in enumerate(row):
            pixels[y+oy][x+ox] = char == '#'
    return pixels

def path_data(pixels):
    runs = []
    for y,row in enumerate(pixels):
        x = 0
        while x < len(row):
            if not row[x]:
                x += 1
                continue
            start = x
            while x < len(row) and row[x]:
                x += 1
            runs.append(f'M{start} {y}h{x-start}v1h{start-x}z')
    return ''.join(runs)

def svg(pixels, fill='currentColor', adaptive=False):
    size = len(pixels)
    style = '<style>path{fill:#111111}@media(prefers-color-scheme:dark){path{fill:#ffffff}}</style>' if adaptive else ''
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {size} {size}" '
            f'width="{size}" height="{size}" fill="{fill}" shape-rendering="crispEdges">'
            f'{style}<path d="{path_data(pixels)}"/></svg>\n')

def raster(pixels, size, color=(17,17,17,255), background=(0,0,0,0)):
    # Nearest-neighbor resampling preserves square pixel steps.
    im = Image.new('RGBA',(len(pixels),len(pixels)),background)
    for y,row in enumerate(pixels):
        for x,on in enumerate(row):
            if on: im.putpixel((x,y),color)
    return im.resize((size,size),Image.Resampling.NEAREST)

def terminal(rows):
    # Each monospace character contains two vertically stacked square pixels.
    width = max(map(len,rows))
    padded = [row.ljust(width,'.') for row in rows]
    if len(padded)%2: padded.append('.'*width)
    chars = {(False,False):' ', (True,False):'▀', (False,True):'▄', (True,True):'█'}
    return '\n'.join(''.join(chars[(a=='#',b=='#')] for a,b in zip(padded[y],padded[y+1])).rstrip() for y in range(0,len(padded),2))+'\n'

def braille(rows):
    # Each braille character holds a 2 x 4 grid of dots. Empty cells stay U+2800,
    # not spaces, so every line keeps the same width in fonts that size them apart.
    width = max(map(len,rows))
    width += width%2
    padded = [row.ljust(width,'.') for row in rows]
    padded += ['.'*width]*(-len(padded)%4)
    dots = [(0,0,0x01),(0,1,0x02),(0,2,0x04),(1,0,0x08),(1,1,0x10),(1,2,0x20),(0,3,0x40),(1,3,0x80)]
    return '\n'.join(''.join(chr(0x2800+sum(bit for dx,dy,bit in dots if padded[y+dy][x+dx]=='#')) for x in range(0,width,2)) for y in range(0,len(padded),4))+'\n'

def main():
    for folder in ['svg','png','terminal','web']:
        (ROOT/folder).mkdir(exist_ok=True)
    master = canvas(MASTER,40,3,3)
    micro = canvas(MICRO,16)
    for name,fill in [('fantail','currentColor'),('fantail-black','#111111'),('fantail-white','#ffffff')]:
        (ROOT/'svg'/f'{name}.svg').write_text(svg(master,fill))
    (ROOT/'svg'/'fantail-micro.svg').write_text(svg(micro))
    (ROOT/'web'/'favicon.svg').write_text(svg(micro,adaptive=True))
    for size in [40,80,120,160,256,320,512,640,1024]:
        for label,color in [('black',(17,17,17,255)),('white',(255,255,255,255))]:
            raster(master,size,color).save(ROOT/'png'/f'fantail-{label}-{size}.png')
    # Light background keeps the favicon legible in both browser themes.
    icons=[raster(micro,s,background=(250,249,246,255)) for s in [16,32,48]]
    icons[-1].save(ROOT/'web'/'favicon.ico',append_images=icons[:-1],sizes=[(16,16),(32,32),(48,48)])
    raster(micro,16).save(ROOT/'png'/'fantail-micro-16.png')
    raster(micro,32).save(ROOT/'png'/'fantail-micro-32.png')
    raster(master,512,background=(250,249,246,255)).save(ROOT/'png'/'fantail-avatar-512.png')
    raster(master,180,background=(250,249,246,255)).save(ROOT/'web'/'apple-touch-icon.png')
    (ROOT/'terminal'/'fantail.txt').write_text(terminal(MASTER),encoding='utf-8')
    (ROOT/'terminal'/'fantail-small.txt').write_text(terminal(MICRO),encoding='utf-8')
    (ROOT/'terminal'/'fantail-braille.txt').write_text(braille(BRAILLE),encoding='utf-8')
    (ROOT/'terminal'/'fantail-ascii.txt').write_text('\n'.join(''.join('##' if p=='#' else '  ' for p in row).rstrip() for row in MICRO)+'\n')
    # Review sheet, using exports themselves.
    sheet=Image.new('RGB',(1000,660),'#f3f2ee')
    d=ImageDraw.Draw(sheet)
    font=ImageFont.truetype('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf',18) if Path('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf').exists() else ImageFont.load_default()
    d.rectangle((510,15,985,450),fill='#171a1e')
    for x,color in [(65,(17,17,17,255)),(565,(255,255,255,255))]:
        im=raster(master,360,color)
        sheet.paste(im,(x,40),im)
    d.text((45,465),'Classic fantail / SVG master',fill='#111111',font=font)
    d.text((545,465),'Reversed / dark backgrounds',fill='#111111',font=font)
    for x,size in [(50,40),(130,80),(260,120)]:
        im=raster(master,size)
        sheet.paste(im,(x,505),im)
    im=raster(micro,96)
    sheet.paste(im,(555,510),im)
    d.text((680,530),'Simplified 16 px mark',fill='#111111',font=font)
    sheet.save(ROOT/'preview.png')
    print('Built SVG, PNG, favicon and terminal assets.')

if __name__=='__main__':
    main()
