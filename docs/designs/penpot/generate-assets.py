#!/usr/bin/env python3
"""Original editable vector artwork for the local Penpot / frontend design."""
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
DEST = ROOT / 'frontend/public/design'
DEST.mkdir(parents=True, exist_ok=True)


def courtyard(night: bool) -> str:
    sky = '#173D36' if night else '#E5EBE2'
    sky2 = '#2C5B4E' if night else '#C5D7C7'
    ink = '#BCD0BB' if night else '#517764'
    water = '#193D38' if night else '#CBDCCC'
    pale = '#D5DBC2' if night else '#F4F1E3'
    wall = '#122F2B' if night else '#D4DDCE'
    roof = '#0D2827' if night else '#355D4F'
    svg = [f'''<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="960" viewBox="0 0 1000 960" fill="none">
<title>Moon courtyard and Canton Tower · original Guangyue artwork</title>
<defs>
 <linearGradient id="sky" x1="0" y1="0" x2="850" y2="850" gradientUnits="userSpaceOnUse"><stop stop-color="{sky}"/><stop offset="1" stop-color="{sky2}"/></linearGradient>
 <linearGradient id="water" x1="500" y1="540" x2="500" y2="900" gradientUnits="userSpaceOnUse"><stop stop-color="{water}"/><stop offset="1" stop-color="{sky}"/></linearGradient>
 <radialGradient id="halo"><stop stop-color="#F1DFAD" stop-opacity=".2"/><stop offset="1" stop-color="#F1DFAD" stop-opacity="0"/></radialGradient>
 <clipPath id="gate"><path d="M150 760V470a350 350 0 0 1 700 0v290Z"/></clipPath>
</defs>
<rect width="1000" height="960" fill="{sky}"/>
<path d="M0 0h1000v960H0Z" fill="url(#sky)"/>
<circle cx="500" cy="410" r="395" fill="{wall}"/>
<circle cx="500" cy="410" r="368" stroke="{ink}" stroke-opacity=".14" stroke-width="2"/>
<g clip-path="url(#gate)">
 <rect x="120" y="80" width="760" height="760" fill="url(#sky)"/>
 <circle cx="390" cy="278" r="150" fill="url(#halo)"/>
 <circle cx="390" cy="278" r="58" fill="{pale}"/>
 <path d="M115 515 235 390 358 482 482 394 610 478 782 349 890 474V680H110Z" fill="{ink}" fill-opacity=".13"/>
 <path d="m110 561 158-99 136 91 144-56 153 78 135-90 118 81v162H110Z" fill="{ink}" fill-opacity=".2"/>
 <g stroke="{ink}" stroke-width="2" stroke-opacity=".55">
  <path d="M615 572c7-42 18-67 26-97 13-43 12-101 1-142h70c-10 41-11 99 1 142 8 30 19 55 26 97Z"/>
  <path d="M646 333c27 61 7 142-15 222M705 333c-27 61-7 142 15 222M657 333c29 59 13 138-15 239M694 333c-28 60-12 139 14 239M637 361h79M641 383h71M645 408h64M645 436h64M640 464h72M632 493h89M622 528h108M615 567h124"/>
  <path d="M650 326h54M652 321h50M660 314h36M672 308v-61M681 308v-76M690 308v-62"/>
 </g>
 <path d="M127 602h732v200H127Z" fill="url(#water)"/>
 <g stroke="{ink}" stroke-opacity=".2" stroke-width="2"><path d="M178 631h238M455 651h181M218 675h270M555 699h180M157 720h200M327 749h295M618 771h101"/></g>
 <path d="M356 615h68l22 54-73 53-25-47Z" fill="{pale}" fill-opacity=".04"/>
 <path d="M583 605h174l-22 84-56 27-51-39Z" fill="{ink}" fill-opacity=".05"/>
 <g stroke="{ink}" stroke-opacity=".32" stroke-width="2"><path d="M346 635h77M368 642h44M390 657h59M344 668h51"/></g>
</g>
<path d="M130 763V470a370 370 0 0 1 740 0v293h-20V470a350 350 0 0 0-700 0v293Z" fill="{pale}" fill-opacity=".15"/>
<path d="M141 759V470a359 359 0 0 1 718 0v289" stroke="{pale}" stroke-opacity=".42" stroke-width="2"/>
<path d="M0 778c130-55 208-14 320-5 210 17 370-25 680 11v176H0Z" fill="{roof}"/>
<path d="m0 848 317-44 92 52-237 74H0Z" fill="{wall}"/>
<path d="m505 758-87 91 219 111h240L564 803Z" fill="{pale}" fill-opacity=".12"/>
<path d="m490 775 41 8 47 21-50 2Zm-31 32 65 4 71 30-77 2Zm-35 35 90 3 104 43-109 4Zm-30 39 116 15 136 59H486Z" fill="{pale}" fill-opacity=".18"/>
<g fill="{roof}">
 <path d="M43 591c-12-37-28-63-44-78 39 8 70 47 60 90ZM85 593c24-33 42-49 75-52-25 39-40 58-64 65ZM23 535c-5-33 0-52 12-71 12 37 14 56 0 84ZM74 527c22-24 46-40 76-40-16 32-36 51-65 54Z"/>
 <path d="M50 806c13-103 31-164 59-261" stroke="{roof}" stroke-width="7"/>
 <path d="M51 748c-35-22-44-40-45-66 24 7 47 27 51 57ZM66 700c22-31 47-43 76-44-14 29-35 43-70 54ZM78 652c-21-27-24-49-18-74 24 21 35 41 27 68ZM107 566c-1-28 4-47 19-63 4 25 5 47-11 70Z"/>
 <path d="M934 0c-22 92-75 131-124 177M967 0c-57 107-94 130-143 164" stroke="{roof}" stroke-width="6"/>
 <path d="M843 145c-33-8-57-23-66-46 37 2 64 20 77 38ZM876 112c9-39 33-60 59-63-9 38-27 58-51 72ZM895 58c-35-10-53-27-54-51 31 4 54 19 63 45ZM817 172c-17-34-17-57-4-78 20 33 24 54 12 72Z"/>
</g>
<g>
 <path d="M746 696h171V477H746Z" fill="{wall}"/>
 <path d="M732 489h206v-18H732Z" fill="{pale}" fill-opacity=".42"/>
 <path d="m699 479 45-21 89-78 90 78 49 21c-49 8-103 3-139-6-38 9-97 14-134 6Z" fill="{roof}"/>
 <path d="m720 471 42-17 72-58 72 58 43 17" stroke="{ink}" stroke-opacity=".7" stroke-width="2"/>
 <path d="M778 511h109v92H778Z" fill="{sky}" stroke="{ink}" stroke-opacity=".6" stroke-width="3"/>
 <g stroke="{ink}" stroke-opacity=".5" stroke-width="2"><path d="M797 511v92M816 511v92M835 511v92M854 511v92M873 511v92M778 534h109M778 557h109M778 580h109"/></g>
 <path d="M739 680h187M737 690h192M732 701h200M725 714h210" stroke="{ink}" stroke-opacity=".6" stroke-width="3"/>
 <path d="M762 489v191M904 489v191" stroke="{ink}" stroke-opacity=".65" stroke-width="7"/>
 <path d="M741 645h180M762 644v28M789 644v28M817 644v28M845 644v28M873 644v28M901 644v28" stroke="{ink}" stroke-opacity=".5" stroke-width="3"/>
 <path d="M729 467v38M723 484h12" stroke="#D59468" stroke-width="2"/>
 <rect x="718" y="494" width="23" height="35" rx="10" fill="#B65D43"/>
 <path d="M721 501h17M721 520h17M729 529v8" stroke="#E4B98E" stroke-width="2"/>
</g>
<g stroke="{ink}" stroke-opacity=".5"><path d="M877 814c-39-33-15-91 33-66 40-22 65 18 39 52" fill="{sky}" stroke-width="3"/><path d="M904 765c-20-31-28-59-10-87M923 767c21-41 47-69 68-72" stroke-width="4"/></g>
<g stroke="{ink}" stroke-opacity=".2" stroke-width="2"><path d="M610 857h170M640 870h87M702 891h115M221 917h155M242 930h91"/></g>''']
    if night:
        svg.append('<g fill="#E8DDAD" fill-opacity=".5"><circle cx="271" cy="201" r="1.5"/><circle cx="480" cy="162" r="1.5"/><circle cx="533" cy="325" r="2"/><circle cx="588" cy="245" r="1"/><circle cx="740" cy="280" r="1.5"/></g>')
    svg.append('</svg>')
    return '\n'.join(svg)


for name, night in [('courtyard-night', True), ('courtyard-day', False)]:
    (DEST / f'{name}.svg').write_text(courtyard(night))

(DEST / 'moon-seal.svg').write_text('''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none"><title>Guangyue moon seal</title><rect width="64" height="64" rx="18" fill="#173D36"/><path d="M42 16a20 20 0 1 0 6 29A18 18 0 0 1 42 16Z" fill="#EAE4C8"/><path d="M31 52V38a8 8 0 0 1 16 0v14M28 52h22M35 40v8M43 40v8" stroke="#173D36" stroke-width="2.5" stroke-linecap="round"/></svg>''')
print('Generated original day/night courtyard and moon seal SVG assets.')
