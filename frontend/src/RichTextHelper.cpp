#include "RichTextHelper.h"

#include <QFont>
#include <QTextBlock>
#include <QTextBlockFormat>
#include <QTextCharFormat>
#include <QTextCursor>
#include <QTextDocument>
#include <QTextList>
#include <QTextListFormat>

RichTextHelper::RichTextHelper(QObject *parent) : QObject(parent) {}

void RichTextHelper::setDocument(QQuickTextDocument *d)
{
    if (m_doc == d)
        return;
    m_doc = d;
    emit documentChanged();
    refreshState();
}

void RichTextHelper::setCursorPosition(int pos)
{
    if (m_cursorPos == pos)
        return;
    m_cursorPos = pos;
    emit cursorChanged();
    refreshState();
}

void RichTextHelper::setSelectionStart(int pos)
{
    if (m_selStart == pos)
        return;
    m_selStart = pos;
    emit cursorChanged();
    refreshState();
}

void RichTextHelper::setSelectionEnd(int pos)
{
    if (m_selEnd == pos)
        return;
    m_selEnd = pos;
    emit cursorChanged();
    refreshState();
}

QTextCursor RichTextHelper::makeCursor() const
{
    QTextDocument *td = textDoc();
    if (!td)
        return {};
    QTextCursor cur(td);
    const int maxPos = std::max(0, td->characterCount() - 1);
    const int start = std::clamp(m_selStart, 0, maxPos);
    const int end = std::clamp(m_selEnd, 0, maxPos);
    if (start != end) {
        cur.setPosition(start);
        cur.setPosition(end, QTextCursor::KeepAnchor);
    } else {
        cur.setPosition(std::clamp(m_cursorPos, 0, maxPos));
    }
    return cur;
}

void RichTextHelper::applyCharFormat(const QTextCharFormat &fmt)
{
    QTextCursor cur = makeCursor();
    if (cur.isNull())
        return;
    if (!cur.hasSelection())
        cur.select(QTextCursor::WordUnderCursor);
    cur.mergeCharFormat(fmt);
    m_hasRichFormatting = true;
    refreshState();
}

void RichTextHelper::setPlainContent(const QString &text)
{
    QTextDocument *td = textDoc();
    if (!td)
        return;
    td->setPlainText(text);
    m_hasRichFormatting = false;
    refreshState();
}

QString RichTextHelper::plainText() const
{
    QTextDocument *td = textDoc();
    return td ? td->toPlainText() : QString();
}

QString RichTextHelper::htmlBody() const
{
    if (!m_hasRichFormatting)
        return {};
    QTextDocument *td = textDoc();
    return td ? td->toHtml() : QString();
}

void RichTextHelper::toggleBold()
{
    QTextCharFormat fmt;
    fmt.setFontWeight(m_bold ? QFont::Normal : QFont::Bold);
    applyCharFormat(fmt);
}

void RichTextHelper::toggleItalic()
{
    QTextCharFormat fmt;
    fmt.setFontItalic(!m_italic);
    applyCharFormat(fmt);
}

void RichTextHelper::toggleUnderline()
{
    QTextCharFormat fmt;
    fmt.setFontUnderline(!m_underline);
    applyCharFormat(fmt);
}

void RichTextHelper::toggleStrikeout()
{
    QTextCharFormat fmt;
    fmt.setFontStrikeOut(!m_strikeout);
    applyCharFormat(fmt);
}

void RichTextHelper::setFontFamily(const QString &family)
{
    QTextCharFormat fmt;
    if (family == QLatin1String("Sans-serif"))
        fmt.setFontFamilies({QStringLiteral("sans-serif")});
    else if (family == QLatin1String("Serif"))
        fmt.setFontFamilies({QStringLiteral("serif")});
    else if (family == QLatin1String("Monospace"))
        fmt.setFontFamilies({QStringLiteral("monospace")});
    else
        fmt.setFontFamilies({family});
    applyCharFormat(fmt);
}

void RichTextHelper::setFontSize(int px)
{
    if (px <= 0)
        return;
    QTextCharFormat fmt;
    fmt.setFontPointSize(px * 0.75); // px -> pt (96 dpi)
    applyCharFormat(fmt);
}

void RichTextHelper::setTextColor(const QColor &color)
{
    QTextCharFormat fmt;
    if (color.isValid())
        fmt.setForeground(color);
    else
        fmt.clearForeground();
    applyCharFormat(fmt);
}

void RichTextHelper::setAlignment(const QString &align)
{
    QTextCursor cur = makeCursor();
    if (cur.isNull())
        return;
    QTextBlockFormat bf;
    if (align == QLatin1String("center"))
        bf.setAlignment(Qt::AlignHCenter);
    else if (align == QLatin1String("right"))
        bf.setAlignment(Qt::AlignRight);
    else
        bf.setAlignment(Qt::AlignLeft);
    cur.mergeBlockFormat(bf);
    m_hasRichFormatting = true;
    refreshState();
}

void RichTextHelper::toggleList(const QString &kind)
{
    QTextCursor cur = makeCursor();
    if (cur.isNull())
        return;
    const QTextListFormat::Style want =
        (kind == QLatin1String("numbered")) ? QTextListFormat::ListDecimal : QTextListFormat::ListDisc;

    cur.beginEditBlock();
    if (QTextList *list = cur.currentList()) {
        if (list->format().style() == want) {
            // Remove from list.
            list->remove(cur.block());
            QTextBlockFormat bf = cur.blockFormat();
            bf.setIndent(0);
            cur.setBlockFormat(bf);
        } else {
            QTextListFormat lf = list->format();
            lf.setStyle(want);
            list->setFormat(lf);
        }
    } else {
        QTextListFormat lf;
        lf.setStyle(want);
        cur.createList(lf);
    }
    cur.endEditBlock();
    m_hasRichFormatting = true;
    refreshState();
}

void RichTextHelper::changeIndent(int delta)
{
    QTextCursor cur = makeCursor();
    if (cur.isNull())
        return;
    QTextBlockFormat bf = cur.blockFormat();
    bf.setIndent(std::max(0, bf.indent() + delta));
    cur.mergeBlockFormat(bf);
    m_hasRichFormatting = true;
    refreshState();
}

void RichTextHelper::setBlockStyle(const QString &style)
{
    QTextCursor cur = makeCursor();
    if (cur.isNull())
        return;

    cur.beginEditBlock();
    if (!cur.hasSelection()) {
        cur.movePosition(QTextCursor::StartOfBlock);
        cur.movePosition(QTextCursor::EndOfBlock, QTextCursor::KeepAnchor);
    }

    QTextBlockFormat bf = cur.blockFormat();
    QTextCharFormat cf;

    if (style == QLatin1String("h1")) {
        bf.setHeadingLevel(1);
        bf.setIndent(0);
        cf.setFontPointSize(18);
        cf.setFontWeight(QFont::Bold);
        cf.setFontItalic(false);
    } else if (style == QLatin1String("h2")) {
        bf.setHeadingLevel(2);
        bf.setIndent(0);
        cf.setFontPointSize(15);
        cf.setFontWeight(QFont::Bold);
        cf.setFontItalic(false);
    } else if (style == QLatin1String("h3")) {
        bf.setHeadingLevel(3);
        bf.setIndent(0);
        cf.setFontPointSize(13);
        cf.setFontWeight(QFont::Bold);
        cf.setFontItalic(false);
    } else if (style == QLatin1String("quote")) {
        bf.setHeadingLevel(0);
        bf.setIndent(1);
        cf.setFontPointSize(10.5);
        cf.setFontWeight(QFont::Normal);
        cf.setFontItalic(true);
    } else if (style == QLatin1String("code")) {
        bf.setHeadingLevel(0);
        bf.setIndent(1);
        cf.setFontFamilies({QStringLiteral("monospace")});
        cf.setFontPointSize(10);
        cf.setFontWeight(QFont::Normal);
        cf.setFontItalic(false);
    } else {
        bf.setHeadingLevel(0);
        bf.setIndent(0);
        cf.setFontPointSize(10.5);
        cf.setFontWeight(QFont::Normal);
        cf.setFontItalic(false);
    }

    cur.setBlockFormat(bf);
    cur.mergeCharFormat(cf);
    cur.endEditBlock();
    m_hasRichFormatting = true;
    refreshState();
}

void RichTextHelper::clearFormatting()
{
    QTextCursor cur = makeCursor();
    if (cur.isNull())
        return;
    cur.beginEditBlock();
    if (!cur.hasSelection())
        cur.select(QTextCursor::WordUnderCursor);
    cur.setCharFormat(QTextCharFormat());
    QTextBlockFormat bf;
    bf.setAlignment(Qt::AlignLeft);
    bf.setIndent(0);
    bf.setHeadingLevel(0);
    cur.setBlockFormat(bf);
    cur.endEditBlock();
    refreshState();
}

void RichTextHelper::refreshState()
{
    QTextCursor cur = makeCursor();
    if (cur.isNull())
        return;
    const QTextCharFormat cf = cur.charFormat();
    const QTextBlockFormat bf = cur.blockFormat();

    m_bold = cf.fontWeight() >= QFont::Bold;
    m_italic = cf.fontItalic();
    m_underline = cf.fontUnderline();
    m_strikeout = cf.fontStrikeOut();

    const QStringList fams = cf.fontFamilies().toStringList();
    if (!fams.isEmpty() && !fams.first().isEmpty()) {
        const QString f = fams.first();
        if (f.compare(QLatin1String("sans-serif"), Qt::CaseInsensitive) == 0)
            m_fontFamily = QStringLiteral("Sans-serif");
        else if (f.compare(QLatin1String("serif"), Qt::CaseInsensitive) == 0)
            m_fontFamily = QStringLiteral("Serif");
        else if (f.compare(QLatin1String("monospace"), Qt::CaseInsensitive) == 0)
            m_fontFamily = QStringLiteral("Monospace");
        else
            m_fontFamily = f;
    } else {
        m_fontFamily = QStringLiteral("Sans-serif");
    }

    const qreal pt = cf.fontPointSize();
    m_fontSize = pt > 0 ? qRound(pt / 0.75) : 14;
    m_textColor = cf.hasProperty(QTextFormat::ForegroundBrush) ? cf.foreground().color() : QColor();

    if (bf.alignment() & Qt::AlignHCenter)
        m_alignment = QStringLiteral("center");
    else if (bf.alignment() & Qt::AlignRight)
        m_alignment = QStringLiteral("right");
    else
        m_alignment = QStringLiteral("left");

    if (QTextList *list = cur.currentList()) {
        m_listStyle = (list->format().style() == QTextListFormat::ListDecimal)
                          ? QStringLiteral("numbered")
                          : QStringLiteral("bullet");
    } else {
        m_listStyle = QStringLiteral("none");
    }

    switch (bf.headingLevel()) {
    case 1: m_blockStyle = QStringLiteral("h1"); break;
    case 2: m_blockStyle = QStringLiteral("h2"); break;
    case 3: m_blockStyle = QStringLiteral("h3"); break;
    default:
        if (bf.indent() > 0 && m_italic)
            m_blockStyle = QStringLiteral("quote");
        else if (bf.indent() > 0 && m_fontFamily == QLatin1String("Monospace"))
            m_blockStyle = QStringLiteral("code");
        else
            m_blockStyle = QStringLiteral("body");
        break;
    }

    emit formatChanged();
}
