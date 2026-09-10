#!/usr/bin/env python3
from pathlib import Path
from xml.sax.saxutils import escape

WIDTH = 1920
HEIGHT = 1080
OUT = Path("deploy/emby/library-posters")
LIBRARIES = [
    ("电视剧追更", "ON AIR · 持续更新", "01", "#9E2A2B", "#FFF4D6", "timeline"),
    ("综艺追更", "NEW SHOWS · 持续更新", "02", "#B44600", "#FFF2C7", "marquee"),
    ("最新电影", "NEW RELEASES · 近期入库", "03", "#E0A100", "#19170F", "ticket"),
    ("电影", "FEATURE FILMS · 电影长片", "04", "#6D1E34", "#FFF1DC", "reel"),
    ("IMAX巨幕", "高码率 · 大文件", "05", "#102A43", "#E8F1F5", "screen"),
    ("电视剧", "SERIES ARCHIVE · 完结剧集", "06", "#164B38", "#F3EBDD", "episodes"),
    ("动漫", "ANIMATION SERIES · 动画剧集", "07", "#006D77", "#F5F0DC", "cel"),
    ("动漫电影", "ANIMATION FILMS · 剧场长片", "08", "#7B2D5F", "#FFF0D8", "frame"),
    ("儿童", "KIDS · 儿童精选", "09", "#F4C430", "#17213A", "blocks"),
    ("综艺", "VARIETY ARCHIVE · 完结节目", "10", "#44355B", "#FFF2C7", "stage"),
    ("纪录片", "DOCUMENTARY · 真实记录", "11", "#3D4F2F", "#F1E8D2", "map"),
    ("合集", "COLLECTIONS · 系列合辑", "12", "#2D2935", "#FFD9C7", "archive"),
]


def glyph(kind: str, ink: str) -> str:
    common = f'fill="none" stroke="{ink}" stroke-width="24" stroke-linecap="square" stroke-linejoin="miter"'
    if kind == "timeline":
        return f'<g {common}><path d="M1260 330H1690M1260 540H1690M1260 750H1690"/><circle cx="1320" cy="330" r="34" fill="{ink}"/><circle cx="1510" cy="540" r="34" fill="{ink}"/><circle cx="1660" cy="750" r="34" fill="{ink}"/><path d="M1320 330L1510 540L1660 750"/></g>'
    if kind == "marquee":
        bulbs = ''.join(f'<circle cx="{x}" cy="{y}" r="18" fill="{ink}"/>' for x in range(1250, 1741, 98) for y in (330, 750))
        return f'<g {common}><rect x="1200" y="280" width="590" height="520"/>{bulbs}<path d="M1300 620L1495 410L1690 620Z"/></g>'
    if kind == "ticket":
        return f'<g {common}><path d="M1200 360H1790V520C1710 520 1710 650 1790 650V810H1200V650C1280 650 1280 520 1200 520Z"/><path d="M1460 390V780" stroke-dasharray="18 24"/><path d="M1530 500H1700M1530 590H1660M1530 680H1720"/></g>'
    if kind == "reel":
        holes = ''.join(f'<circle cx="{x}" cy="{y}" r="48"/>' for x, y in ((1460,390),(1610,470),(1560,640),(1380,650),(1330,480)))
        return f'<g {common}><circle cx="1470" cy="520" r="280"/><circle cx="1470" cy="520" r="52" fill="{ink}"/>{holes}<path d="M1660 730L1810 830"/></g>'
    if kind == "screen":
        return f'<g {common}><path d="M1170 300H1810V760H1170Z"/><path d="M1220 360H1760V700H1220Z"/><path d="M1260 835H1720"/><path d="M1300 780L1260 835M1680 780L1720 835"/><path d="M1280 640L1410 500L1510 600L1660 420L1750 520"/></g>'
    if kind == "episodes":
        return f'<g {common}><rect x="1210" y="310" width="510" height="150"/><rect x="1270" y="505" width="510" height="150"/><rect x="1150" y="700" width="510" height="150"/><path d="M1320 385H1600M1380 580H1660M1260 775H1540"/></g>'
    if kind == "cel":
        return f'<g {common}><rect x="1180" y="290" width="490" height="520"/><rect x="1260" y="230" width="490" height="520"/><path d="M1320 610L1430 430L1530 550L1680 350"/><circle cx="1420" cy="365" r="45"/><path d="M1170 860L1300 760M1380 860L1510 760M1590 860L1720 760"/></g>'
    if kind == "frame":
        return f'<g {common}><rect x="1180" y="300" width="600" height="470"/><path d="M1260 690L1420 480L1530 600L1680 390L1740 470"/><circle cx="1350" cy="410" r="42"/><path d="M1180 835H1780" stroke-dasharray="34 22"/></g>'
    if kind == "blocks":
        return f'<g {common}><rect x="1190" y="600" width="220" height="220"/><circle cx="1300" cy="710" r="54"/><path d="M1430 820V500H1750V820Z"/><path d="M1510 720L1590 590L1680 720Z"/><path d="M1240 500L1340 330L1440 500Z"/></g>'
    if kind == "stage":
        return f'<g {common}><path d="M1210 300L1370 760M1770 300L1610 760"/><circle cx="1210" cy="300" r="55"/><circle cx="1770" cy="300" r="55"/><path d="M1320 780Q1490 640 1660 780"/><path d="M1240 840H1740"/></g>'
    if kind == "map":
        return f'<g {common}><path d="M1200 320L1380 260L1560 340L1760 260V790L1560 870L1380 790L1200 870Z"/><path d="M1380 260V790M1560 340V870"/><circle cx="1510" cy="520" r="85"/><path d="M1510 605V730M1450 730H1570"/></g>'
    if kind == "archive":
        return f'<g {common}><rect x="1180" y="300" width="520" height="150"/><rect x="1250" y="490" width="520" height="150"/><rect x="1320" y="680" width="520" height="150"/><path d="M1280 375H1570M1350 565H1640M1420 755H1710"/></g>'
    raise ValueError(f"unknown library glyph: {kind}")


def poster(title: str, subtitle: str, index: str, background: str, ink: str, kind: str) -> str:
    perforations = ''.join(f'<rect x="{x}" y="44" width="70" height="28" fill="{ink}" opacity=".82"/>' for x in range(50, 1900, 105))
    perforations += ''.join(f'<rect x="{x}" y="1008" width="70" height="28" fill="{ink}" opacity=".82"/>' for x in range(50, 1900, 105))
    grid = ''.join(f'<path d="M0 {y}H1920"/>' for y in range(160, 961, 100)) + ''.join(f'<path d="M{x} 0V1080"/>' for x in range(100, 1901, 100))
    title_size = 150 if len(title) <= 5 else 124
    return f'''<svg xmlns="http://www.w3.org/2000/svg" width="{WIDTH}" height="{HEIGHT}" viewBox="0 0 {WIDTH} {HEIGHT}">
  <rect width="1920" height="1080" fill="{background}"/>
  <g stroke="{ink}" stroke-width="2" opacity=".09">{grid}</g>
  {perforations}
  <text x="110" y="160" fill="{ink}" font-family="Avenir Next Condensed, sans-serif" font-size="30" font-weight="700" letter-spacing="8">EMBY MEDIA LIBRARY / {index}</text>
  <rect x="110" y="245" width="105" height="18" fill="{ink}"/>
  <text x="110" y="535" fill="{ink}" font-family="STHeiti, Hiragino Sans GB, sans-serif" font-size="{title_size}" font-weight="700" letter-spacing="4">{escape(title)}</text>
  <text x="118" y="625" fill="{ink}" font-family="STHeiti, Hiragino Sans GB, sans-serif" font-size="34" font-weight="500" letter-spacing="5">{escape(subtitle)}</text>
  <path d="M110 760H930" stroke="{ink}" stroke-width="8"/>
  <text x="110" y="830" fill="{ink}" font-family="Avenir Next Condensed, sans-serif" font-size="28" font-weight="700" letter-spacing="6">CATALOGUED MEDIA ARCHIVE</text>
  <text x="110" y="890" fill="{ink}" font-family="Avenir Next Condensed, sans-serif" font-size="22" font-weight="600" letter-spacing="4" opacity=".78">115 / STRM / EMBY</text>
  {glyph(kind, ink)}
</svg>
'''


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    for title, subtitle, index, background, ink, kind in LIBRARIES:
        (OUT / f"{index}-{title}.svg").write_text(poster(title, subtitle, index, background, ink, kind))


if __name__ == "__main__":
    main()
