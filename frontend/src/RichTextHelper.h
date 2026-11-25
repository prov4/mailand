#pragma once

#include <QColor>
#include <QObject>
#include <QQuickTextDocument>
#include <QString>
#include <QTextCharFormat>
#include <QTextCursor>
#include <QtQml/qqmlregistration.h>

// Bridges a QML TextArea's QQuickTextDocument to QTextCursor rich-text formatting
// (bold, italic, underline, strikeout, font family/size, text color, alignment,
// bullet/numbered lists, indentation, and paragraph styles).
class RichTextHelper : public QObject
{
    Q_OBJECT
    QML_ELEMENT

    Q_PROPERTY(QQuickTextDocument *document READ document WRITE setDocument NOTIFY documentChanged FINAL)
    Q_PROPERTY(int cursorPosition READ cursorPosition WRITE setCursorPosition NOTIFY cursorChanged FINAL)
    Q_PROPERTY(int selectionStart READ selectionStart WRITE setSelectionStart NOTIFY cursorChanged FINAL)
    Q_PROPERTY(int selectionEnd READ selectionEnd WRITE setSelectionEnd NOTIFY cursorChanged FINAL)

    Q_PROPERTY(bool bold READ bold NOTIFY formatChanged FINAL)
    Q_PROPERTY(bool italic READ italic NOTIFY formatChanged FINAL)
    Q_PROPERTY(bool underline READ underline NOTIFY formatChanged FINAL)
    Q_PROPERTY(bool strikeout READ strikeout NOTIFY formatChanged FINAL)
    Q_PROPERTY(QString fontFamily READ fontFamily NOTIFY formatChanged FINAL)
    Q_PROPERTY(int fontSize READ fontSize NOTIFY formatChanged FINAL)
    Q_PROPERTY(QColor textColor READ textColor NOTIFY formatChanged FINAL)
    Q_PROPERTY(QString alignment READ alignment NOTIFY formatChanged FINAL)
    Q_PROPERTY(QString listStyle READ listStyle NOTIFY formatChanged FINAL)
    Q_PROPERTY(QString blockStyle READ blockStyle NOTIFY formatChanged FINAL)
    Q_PROPERTY(bool hasRichFormatting READ hasRichFormatting NOTIFY formatChanged FINAL)

public:
    explicit RichTextHelper(QObject *parent = nullptr);

    QQuickTextDocument *document() const { return m_doc; }
    void setDocument(QQuickTextDocument *d);

    int cursorPosition() const { return m_cursorPos; }
    void setCursorPosition(int pos);
    int selectionStart() const { return m_selStart; }
    void setSelectionStart(int pos);
    int selectionEnd() const { return m_selEnd; }
    void setSelectionEnd(int pos);

    bool bold() const { return m_bold; }
    bool italic() const { return m_italic; }
    bool underline() const { return m_underline; }
    bool strikeout() const { return m_strikeout; }
    QString fontFamily() const { return m_fontFamily; }
    int fontSize() const { return m_fontSize; }
    QColor textColor() const { return m_textColor; }
    QString alignment() const { return m_alignment; }
    QString listStyle() const { return m_listStyle; }
    QString blockStyle() const { return m_blockStyle; }
    bool hasRichFormatting() const { return m_hasRichFormatting; }

    Q_INVOKABLE void setPlainContent(const QString &text);
    Q_INVOKABLE QString plainText() const;
    Q_INVOKABLE QString htmlBody() const;

    Q_INVOKABLE void toggleBold();
    Q_INVOKABLE void toggleItalic();
    Q_INVOKABLE void toggleUnderline();
    Q_INVOKABLE void toggleStrikeout();
    Q_INVOKABLE void setFontFamily(const QString &family);
    Q_INVOKABLE void setFontSize(int px);
    Q_INVOKABLE void setTextColor(const QColor &color);
    Q_INVOKABLE void setAlignment(const QString &align);
    Q_INVOKABLE void toggleList(const QString &kind);
    Q_INVOKABLE void changeIndent(int delta);
    Q_INVOKABLE void setBlockStyle(const QString &style);
    Q_INVOKABLE void clearFormatting();

signals:
    void documentChanged();
    void cursorChanged();
    void formatChanged();

private:
    QTextDocument *textDoc() const { return m_doc ? m_doc->textDocument() : nullptr; }
    QTextCursor makeCursor() const;
    void applyCharFormat(const QTextCharFormat &fmt);
    void refreshState();

    QQuickTextDocument *m_doc = nullptr;
    int m_cursorPos = 0;
    int m_selStart = 0;
    int m_selEnd = 0;

    bool m_bold = false;
    bool m_italic = false;
    bool m_underline = false;
    bool m_strikeout = false;
    QString m_fontFamily = QStringLiteral("Sans-serif");
    int m_fontSize = 14;
    QColor m_textColor;
    QString m_alignment = QStringLiteral("left");
    QString m_listStyle = QStringLiteral("none");
    QString m_blockStyle = QStringLiteral("body");
    bool m_hasRichFormatting = false;
};
