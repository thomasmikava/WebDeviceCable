"""Development fixtures only: uv run --with python-docx --with openpyxl --with reportlab --with xlwt scripts/generate-attachment-fixtures.py"""
from pathlib import Path
from struct import pack_into
from docx import Document
from openpyxl import Workbook
from reportlab.pdfgen.canvas import Canvas
import xlwt

out = Path(__file__).resolve().parents[1] / 'internal/bridge/testdata/attachments'
out.mkdir(parents=True, exist_ok=True)
doc = Document()
doc.add_paragraph('The attachment marker is saffron.')
doc.save(out / 'notes.docx')
workbook = Workbook()
workbook.active.append(['Task', 'Owner'])
workbook.active.append(['Read saffron report', 'Avery'])
workbook.save(out / 'tasks.xlsx')
legacy = xlwt.Workbook()
legacy.add_sheet('Tasks').write(0, 0, 'Read saffron report')
legacy.save(str(out / 'tasks.xls'))
pdf = Canvas(str(out / 'notes.pdf'))
pdf.drawString(50, 750, 'The attachment marker is saffron.')
pdf.save()

# Minimal MS-DOC Word 97 compound file: one UTF-16 main-text piece,
# two 4096-byte streams, directory sector and FAT. No macros or external links.
end, free, fat_sector = 0xFFFFFFFE, 0xFFFFFFFF, 0xFFFFFFFD
text = 'The attachment marker is saffron.\r'.encode('utf-16le')
word, table = bytearray(4096), bytearray(4096)
word[1024:1024 + len(text)] = text
for at, value in [(0, 0xA5EC), (2, 0x00C1), (6, 0x0409), (10, (1 << 2) | (1 << 9) | (1 << 12)), (12, 0x00BF), (32, 14), (62, 22), (152, 93)]:
    pack_into('<H', word, at, value)
for at, value in [(24, 1024), (28, 1024 + len(text)), (64, 4096), (76, len(text) // 2), (418, 0), (422, 21)]:
    pack_into('<I', word, at, value)
table[0] = 2
pack_into('<I', table, 1, 16)
pack_into('<II', table, 5, 0, len(text) // 2)
pack_into('<I', table, 15, 1024)
data = bytearray(512 * 19)
data[:8] = bytes.fromhex('d0cf11e0a1b11ae1')
for at, value in [(24, 0x3E), (26, 3), (28, 0xFFFE), (30, 9), (32, 6)]:
    pack_into('<H', data, at, value)
for at, value in [(44, 1), (48, 0), (56, 4096), (60, end), (68, end)]:
    pack_into('<I', data, at, value)
for at in range(76, 512, 4):
    pack_into('<I', data, at, free)
pack_into('<I', data, 76, 17)
def entry(at, name, kind, right, child, start, size):
    encoded = (name + '\0').encode('utf-16le')
    data[at:at+len(encoded)] = encoded
    pack_into('<HBBIIIII', data, at+64, len(encoded), kind, 1, free, right, child, 0, 0)
    pack_into('<IQ', data, at+116, start, size)
entry(512, 'Root Entry', 5, free, 1, end, 0)
entry(640, 'WordDocument', 2, 2, free, 1, 4096)
entry(768, '1Table', 2, free, free, 9, 4096)
data[1024:5120], data[5120:9216] = word, table
for sector in range(128):
    value = sector+1 if 1 <= sector < 8 or 9 <= sector < 16 else end if sector in [0,8,16] else fat_sector if sector == 17 else free
    pack_into('<I', data, 9216+4*sector, value)
(out / 'notes.doc').write_bytes(data)

# Large, unambiguous pixels for native visual-input/tool-result smoke checks.
# PNG encoding uses only the standard library; no image-generation API is used.
import struct
import zlib
size = 384
rows = []
for y in range(size):
    row = bytearray([0])
    for x in range(size):
        row.extend((230, 30, 30) if (x - 192) ** 2 + (y - 192) ** 2 < 115 ** 2 else (255, 255, 255))
    rows.append(row)
def png_chunk(name, payload):
    return struct.pack('>I', len(payload)) + name + payload + struct.pack('>I', zlib.crc32(name + payload))
(out / 'visual.png').write_bytes(b'\x89PNG\r\n\x1a\n' + png_chunk(b'IHDR', struct.pack('>IIBBBBB', size, size, 8, 2, 0, 0, 0)) + png_chunk(b'IDAT', zlib.compress(b''.join(rows))) + png_chunk(b'IEND', b''))
