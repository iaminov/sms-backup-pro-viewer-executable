import os
import sys
import re
import json
from datetime import datetime

from reportlab.lib.pagesizes import letter
from reportlab.lib import colors
from reportlab.platypus import (
    SimpleDocTemplate, Paragraph, Spacer, Table, TableStyle, HRFlowable
)
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.pdfgen import canvas

class NumberedCanvas(canvas.Canvas):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._saved_page_states = []

    def showPage(self):
        self._saved_page_states.append(dict(self.__dict__))
        self._startPage()

    def save(self):
        num_pages = len(self._saved_page_states)
        for state in self._saved_page_states:
            self.__dict__.update(state)
            self.draw_page_decorations(num_pages)
            super().showPage()
        super().save()

    def draw_page_decorations(self, page_count):
        self.saveState()
        self.setFont("Helvetica", 8)
        self.setFillColor(colors.HexColor("#64748b"))
        
        # Running Header on page > 1
        if self._pageNumber > 1:
            self.drawString(40, 760, "Conversation Transcript — sms-backup-pro-viewer-executable")
            self.drawRightString(letter[0] - 40, 760, "Session ID: 5a6229f5-873c-4ed8-a447-b3456c57f836")
            self.setStrokeColor(colors.HexColor("#cbd5e1"))
            self.setLineWidth(0.5)
            self.line(40, 752, letter[0] - 40, 752)
        
        # Running Footer on all pages
        self.setStrokeColor(colors.HexColor("#cbd5e1"))
        self.setLineWidth(0.5)
        self.line(40, 38, letter[0] - 40, 38)
        
        gen_time = datetime.now().strftime("%Y-%m-%d %H:%M")
        self.drawString(40, 26, f"Generated: {gen_time} | Antigravity AI & User Session")
        self.drawRightString(letter[0] - 40, 26, f"Page {self._pageNumber} of {page_count}")
        self.restoreState()


def is_transient_status(msg):
    s = msg.strip()
    if len(s) > 250:
        return False
    lower = s.lower()
    if re.search(r'^(task|i have launched|i am waiting|waiting for|i will wait|i am monitoring|the upload of)', lower):
        if "completed" not in lower and "finished" not in lower and "yes" not in lower:
            return True
        if lower.startswith("waiting for") or lower.startswith("i will wait") or lower.startswith("i am monitoring"):
            return True
    if re.match(r'^task\s+`?task-\d+`?\s+has been launched', lower):
        return True
    return False


def clean_user_text(raw):
    m = re.search(r'<USER_REQUEST>\s*(.*?)\s*(?:</USER_REQUEST>|$)', raw, re.DOTALL)
    text = m.group(1) if m else raw
    text = re.sub(r'<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>', '', text, flags=re.DOTALL)
    text = re.sub(r'/(?:[A-Za-z]:[/\\][^\n\r]+)$', '', text.strip()).strip()
    return text.strip()


def format_timestamp(iso_str):
    if not iso_str:
        return ""
    try:
        clean = iso_str.replace("Z", "+00:00")
        dt = datetime.fromisoformat(clean)
        return dt.strftime("%b %d, %Y • %I:%M:%S %p UTC")
    except Exception:
        return iso_str[:19].replace("T", " ")


def escape_html(text):
    text = text.replace("&", "&amp;")
    text = text.replace("<", "&lt;")
    text = text.replace(">", "&gt;")
    return text


def inline_markdown_to_xml(text):
    placeholders = []
    
    # 1. Protect inline code `...`
    def code_sub(m):
        raw_code = m.group(1)
        esc = escape_html(raw_code)
        placeholders.append(f'<font face="Courier" color="#0f766e"><b>{esc}</b></font>')
        return f"___CODEPH_{len(placeholders)-1}___"
    
    text = re.sub(r'`([^`\n]+)`', code_sub, text)
    
    # 2. Escape HTML for the rest of text
    text = escape_html(text)
    
    # 3. Format bold & italic
    text = re.sub(r'\*\*\*([^\*\n]+)\*\*\*', r'<b><i>\1</i></b>', text)
    text = re.sub(r'\*\*([^\*\n]+)\*\*', r'<b>\1</b>', text)
    text = re.sub(r'(?<!\*)\*([^\*\n]+)\*(?!\*)', r'<i>\1</i>', text)
    
    # 4. Format links [label](url)
    text = re.sub(r'\[([^\]\n]+)\]\(([^)\n]+)\)', r'<b>\1</b> (<font color="#2563eb">\2</font>)', text)
    
    # 5. Restore code placeholders
    for idx, ph in enumerate(placeholders):
        text = text.replace(f"___CODEPH_{idx}___", ph)
        
    return text


def markdown_to_flowables(md_text, styles):
    flowables = []
    lines = md_text.split("\n")
    i = 0
    in_code_block = False
    code_lines = []
    
    while i < len(lines):
        line = lines[i]
        stripped = line.strip()

        # Code block fence
        if stripped.startswith("```"):
            if not in_code_block:
                in_code_block = True
                code_lines = []
            else:
                in_code_block = False
                code_content = "\n".join(code_lines)
                code_escaped = escape_html(code_content).replace("\n", "<br/>").replace(" ", "&nbsp;")
                if not code_escaped.strip():
                    code_escaped = "&nbsp;"
                p = Paragraph(f"<font face='Courier' size=8 color='#1e293b'>{code_escaped}</font>", styles['CodeBlock'])
                t = Table([[p]], colWidths=[letter[0] - 80])
                t.setStyle(TableStyle([
                    ('BACKGROUND', (0,0), (-1,-1), colors.HexColor("#f1f5f9")),
                    ('BOX', (0,0), (-1,-1), 0.5, colors.HexColor("#cbd5e1")),
                    ('TOPPADDING', (0,0), (-1,-1), 6),
                    ('BOTTOMPADDING', (0,0), (-1,-1), 6),
                    ('LEFTPADDING', (0,0), (-1,-1), 10),
                    ('RIGHTPADDING', (0,0), (-1,-1), 10),
                ]))
                flowables.append(t)
                flowables.append(Spacer(1, 6))
            i += 1
            continue

        if in_code_block:
            code_lines.append(line)
            i += 1
            continue

        if not stripped:
            i += 1
            continue

        # Horizontal rule
        if stripped in ("---", "***", "___"):
            flowables.append(Spacer(1, 4))
            flowables.append(HRFlowable(width="100%", thickness=0.5, color=colors.HexColor("#e2e8f0"), spaceAfter=6, spaceBefore=4))
            i += 1
            continue

        # Headers
        if stripped.startswith("### "):
            header_text = inline_markdown_to_xml(stripped[4:])
            flowables.append(Paragraph(header_text, styles['CustomHeading3']))
            i += 1
            continue
        elif stripped.startswith("## "):
            header_text = inline_markdown_to_xml(stripped[3:])
            flowables.append(Paragraph(header_text, styles['CustomHeading2']))
            i += 1
            continue
        elif stripped.startswith("# "):
            header_text = inline_markdown_to_xml(stripped[2:])
            flowables.append(Paragraph(header_text, styles['CustomHeading1']))
            i += 1
            continue

        # Bullet list items (- or *)
        if stripped.startswith(("- ", "* ")):
            bullet_text = inline_markdown_to_xml(stripped[2:])
            flowables.append(Paragraph(f"• {bullet_text}", styles['BulletItem']))
            i += 1
            continue

        # Numbered list items (e.g. 1. or 2.)
        m_num = re.match(r'^(\d+)\.\s+(.*)$', stripped)
        if m_num:
            num_str, num_rest = m_num.groups()
            item_text = inline_markdown_to_xml(num_rest)
            flowables.append(Paragraph(f"<b>{num_str}.</b> {item_text}", styles['NumberedItem']))
            i += 1
            continue

        # Regular paragraph
        p_text = inline_markdown_to_xml(stripped)
        flowables.append(Paragraph(p_text, styles['CustomBody']))
        i += 1

    return flowables


def build_transcript_pdf():
    jsonl_path = r"C:\Users\iamin\.gemini\antigravity-acp\brain\5a6229f5-873c-4ed8-a447-b3456c57f836\.system_generated\logs\transcript_full.jsonl"
    output_pdf = r"C:\Users\iamin\PycharmProjects\sms-backup-pro-viewer-executable\Conversation_Transcript_sms-backup-pro-viewer-executable.pdf"

    with open(jsonl_path, "r", encoding="utf-8") as f:
        lines = [json.loads(l) for l in f]

    user_indices = []
    for idx, o in enumerate(lines):
        if o.get('type') == 'USER_INPUT' and o.get('source') == 'USER_EXPLICIT':
            user_indices.append(idx)

    turns = []
    for turn_num, u_idx in enumerate(user_indices):
        u_obj = lines[u_idx]
        next_u_idx = user_indices[turn_num + 1] if turn_num + 1 < len(user_indices) else len(lines)
        
        asst_msgs = []
        for j in range(u_idx + 1, next_u_idx):
            o = lines[j]
            if o.get('type') == 'PLANNER_RESPONSE' and o.get('source') == 'MODEL':
                c = o.get('content', '').strip()
                if c:
                    asst_msgs.append((j, o.get('created_at', ''), c))
        
        u_text = clean_user_text(u_obj.get('content', ''))
        u_ts = u_obj.get('created_at', '')
        
        substantive = [m for m in asst_msgs if not is_transient_status(m[2])]
        if substantive:
            a_text = "\n\n".join([m[2] for m in substantive])
            a_ts = substantive[-1][1]
        elif asst_msgs:
            a_text = asst_msgs[-1][2]
            a_ts = asst_msgs[-1][1]
        else:
            if turn_num + 1 == len(user_indices):
                a_text = "*[Generating full session transcript PDF document and compiling complete dialogue history]*"
                a_ts = u_ts
            else:
                a_text = "*[User provided follow-up prompt]*"
                a_ts = u_ts

        turns.append({
            'turn': turn_num + 1,
            'user_text': u_text,
            'user_ts': u_ts,
            'asst_text': a_text,
            'asst_ts': a_ts,
        })

    # Prepare document
    doc = SimpleDocTemplate(
        output_pdf,
        pagesize=letter,
        leftMargin=40,
        rightMargin=40,
        topMargin=45,
        bottomMargin=45
    )

    styles = getSampleStyleSheet()

    styles.add(ParagraphStyle('DocTitle', parent=styles['Normal'], fontName='Helvetica-Bold', fontSize=22, leading=26, textColor=colors.HexColor('#0f172a')))
    styles.add(ParagraphStyle('DocSubtitle', parent=styles['Normal'], fontName='Helvetica', fontSize=10, leading=14, textColor=colors.HexColor('#475569')))
    styles.add(ParagraphStyle('MetaLabel', parent=styles['Normal'], fontName='Helvetica-Bold', fontSize=8, leading=11, textColor=colors.HexColor('#64748b')))
    styles.add(ParagraphStyle('MetaValue', parent=styles['Normal'], fontName='Helvetica', fontSize=8, leading=11, textColor=colors.HexColor('#0f172a')))

    styles.add(ParagraphStyle('BadgeUser', parent=styles['Normal'], fontName='Helvetica-Bold', fontSize=9, leading=12, textColor=colors.HexColor('#1d4ed8')))
    styles.add(ParagraphStyle('BadgeAsst', parent=styles['Normal'], fontName='Helvetica-Bold', fontSize=9, leading=12, textColor=colors.HexColor('#334155')))
    styles.add(ParagraphStyle('TimeText', parent=styles['Normal'], fontName='Helvetica', fontSize=8, leading=12, textColor=colors.HexColor('#64748b'), alignment=2))

    styles.add(ParagraphStyle('CustomHeading1', parent=styles['Normal'], fontName='Helvetica-Bold', fontSize=14, leading=18, textColor=colors.HexColor('#0f172a'), spaceBefore=8, spaceAfter=4))
    styles.add(ParagraphStyle('CustomHeading2', parent=styles['Normal'], fontName='Helvetica-Bold', fontSize=12, leading=16, textColor=colors.HexColor('#1e293b'), spaceBefore=7, spaceAfter=3))
    styles.add(ParagraphStyle('CustomHeading3', parent=styles['Normal'], fontName='Helvetica-Bold', fontSize=10, leading=14, textColor=colors.HexColor('#334155'), spaceBefore=6, spaceAfter=2))

    styles.add(ParagraphStyle('CustomBody', parent=styles['Normal'], fontName='Helvetica', fontSize=9, leading=13.5, textColor=colors.HexColor('#1e293b'), spaceAfter=5))
    styles.add(ParagraphStyle('BulletItem', parent=styles['Normal'], fontName='Helvetica', fontSize=9, leading=13.5, textColor=colors.HexColor('#1e293b'), leftIndent=12, spaceAfter=3))
    styles.add(ParagraphStyle('NumberedItem', parent=styles['Normal'], fontName='Helvetica', fontSize=9, leading=13.5, textColor=colors.HexColor('#1e293b'), leftIndent=12, spaceAfter=3))
    styles.add(ParagraphStyle('CodeBlock', parent=styles['Normal'], fontName='Courier', fontSize=8, leading=10.5, textColor=colors.HexColor('#1e293b')))

    story = []

    # Title Banner
    story.append(Paragraph("Full Conversation Transcript", styles['DocTitle']))
    story.append(Spacer(1, 4))
    story.append(Paragraph("Complete Engineering Session Dialogue & Implementation Log", styles['DocSubtitle']))
    story.append(Spacer(1, 10))

    # Metadata Table
    meta_data = [
        [
            Paragraph("<b>Project:</b>", styles['MetaLabel']),
            Paragraph("sms-backup-pro-viewer-executable", styles['MetaValue']),
            Paragraph("<b>Session ID:</b>", styles['MetaLabel']),
            Paragraph("5a6229f5-873c-4ed8-a447-b3456c57f836", styles['MetaValue']),
        ],
        [
            Paragraph("<b>Total Turns:</b>", styles['MetaLabel']),
            Paragraph(f"{len(turns)} Interactive Turns", styles['MetaValue']),
            Paragraph("<b>Exported At:</b>", styles['MetaLabel']),
            Paragraph(datetime.now().strftime("%Y-%m-%d %I:%M:%S %p"), styles['MetaValue']),
        ],
    ]
    meta_table = Table(meta_data, colWidths=[65, 205, 75, 187])
    meta_table.setStyle(TableStyle([
        ('BACKGROUND', (0,0), (-1,-1), colors.HexColor("#f8fafc")),
        ('BOX', (0,0), (-1,-1), 0.5, colors.HexColor("#e2e8f0")),
        ('INNERGRID', (0,0), (-1,-1), 0.5, colors.HexColor("#f1f5f9")),
        ('TOPPADDING', (0,0), (-1,-1), 5),
        ('BOTTOMPADDING', (0,0), (-1,-1), 5),
        ('LEFTPADDING', (0,0), (-1,-1), 8),
        ('RIGHTPADDING', (0,0), (-1,-1), 8),
    ]))
    story.append(meta_table)
    story.append(Spacer(1, 16))

    usable_width = letter[0] - 80

    for t in turns:
        turn_num = t['turn']
        user_text = t['user_text']
        user_ts = format_timestamp(t['user_ts'])
        asst_text = t['asst_text']
        asst_ts = format_timestamp(t['asst_ts'])

        # --- USER MESSAGE BLOCK ---
        u_badge = Paragraph(f"👤 USER  <font size=7 color='#64748b'>[Turn {turn_num}]</font>", styles['BadgeUser'])
        u_time = Paragraph(user_ts, styles['TimeText'])
        u_header_table = Table([[u_badge, u_time]], colWidths=[usable_width * 0.5, usable_width * 0.5])
        u_header_table.setStyle(TableStyle([
            ('BACKGROUND', (0,0), (-1,-1), colors.HexColor("#eff6ff")),
            ('BOX', (0,0), (-1,-1), 0.5, colors.HexColor("#bfdbfe")),
            ('TOPPADDING', (0,0), (-1,-1), 4),
            ('BOTTOMPADDING', (0,0), (-1,-1), 4),
            ('LEFTPADDING', (0,0), (-1,-1), 8),
            ('RIGHTPADDING', (0,0), (-1,-1), 8),
        ]))

        u_content_flowables = markdown_to_flowables(user_text, styles)
        
        # User message wrapper table
        u_body_cells = [[f] for f in u_content_flowables]
        u_body_table = Table(u_body_cells, colWidths=[usable_width])
        u_body_table.setStyle(TableStyle([
            ('BACKGROUND', (0,0), (-1,-1), colors.HexColor("#f8fafc")),
            ('BOX', (0,0), (-1,-1), 0.5, colors.HexColor("#dbeafe")),
            ('LINELEFT', (0,0), (-1,-1), 2.5, colors.HexColor("#3b82f6")),
            ('TOPPADDING', (0,0), (-1,-1), 4),
            ('BOTTOMPADDING', (0,0), (-1,-1), 4),
            ('LEFTPADDING', (0,0), (-1,-1), 10),
            ('RIGHTPADDING', (0,0), (-1,-1), 10),
        ]))

        story.append(u_header_table)
        story.append(u_body_table)
        story.append(Spacer(1, 8))

        # --- ASSISTANT MESSAGE BLOCK ---
        a_badge = Paragraph(f"🤖 ASSISTANT  <font size=7 color='#64748b'>[Turn {turn_num}]</font>", styles['BadgeAsst'])
        a_time = Paragraph(asst_ts, styles['TimeText'])
        a_header_table = Table([[a_badge, a_time]], colWidths=[usable_width * 0.5, usable_width * 0.5])
        a_header_table.setStyle(TableStyle([
            ('BACKGROUND', (0,0), (-1,-1), colors.HexColor("#f1f5f9")),
            ('BOX', (0,0), (-1,-1), 0.5, colors.HexColor("#cbd5e1")),
            ('TOPPADDING', (0,0), (-1,-1), 4),
            ('BOTTOMPADDING', (0,0), (-1,-1), 4),
            ('LEFTPADDING', (0,0), (-1,-1), 8),
            ('RIGHTPADDING', (0,0), (-1,-1), 8),
        ]))

        a_content_flowables = markdown_to_flowables(asst_text, styles)
        
        a_body_cells = [[f] for f in a_content_flowables]
        a_body_table = Table(a_body_cells, colWidths=[usable_width])
        a_body_table.setStyle(TableStyle([
            ('BACKGROUND', (0,0), (-1,-1), colors.white),
            ('BOX', (0,0), (-1,-1), 0.5, colors.HexColor("#e2e8f0")),
            ('LINELEFT', (0,0), (-1,-1), 2.5, colors.HexColor("#64748b")),
            ('TOPPADDING', (0,0), (-1,-1), 4),
            ('BOTTOMPADDING', (0,0), (-1,-1), 4),
            ('LEFTPADDING', (0,0), (-1,-1), 10),
            ('RIGHTPADDING', (0,0), (-1,-1), 10),
        ]))

        story.append(a_header_table)
        story.append(a_body_table)
        story.append(Spacer(1, 14))

    doc.build(story, canvasmaker=NumberedCanvas)
    print(f"SUCCESS: Generated PDF at {output_pdf}")
    return output_pdf

if __name__ == '__main__':
    build_transcript_pdf()
