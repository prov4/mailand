#pragma once

#include <QAbstractListModel>
#include <QDateTime>
#include <QString>
#include <QTimer>
#include <QVariantMap>
#include <QtQml/qqmlregistration.h>

#include <vector>

class MessageModel : public QAbstractListModel
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString folderId READ folderId WRITE setFolderId NOTIFY folderIdChanged FINAL)
    Q_PROPERTY(QString accountId READ accountId WRITE setAccountId NOTIFY accountIdChanged FINAL)
    Q_PROPERTY(QString searchQuery READ searchQuery WRITE setSearchQuery NOTIFY searchQueryChanged FINAL)
    Q_PROPERTY(bool unreadOnly READ unreadOnly WRITE setUnreadOnly NOTIFY unreadOnlyChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY loadingChanged FINAL)
    Q_PROPERTY(int totalCount READ totalCount NOTIFY totalCountChanged FINAL)
    Q_PROPERTY(int count READ count NOTIFY countChanged FINAL)

public:
    enum Roles {
        MessageIdRole = Qt::UserRole + 1,
        SubjectRole,
        FromNameRole,
        FromAddressRole,
        SenderRole,
        TimestampRole,
        DateTextRole,
        IsReadRole,
        IsFlaggedRole,
        IsAnsweredRole,
        HasAttachmentsRole,
        SnippetRole,
        SnippetRichRole,
    };

    struct Item {
        QString id, subject, fromName, fromAddress, snippetRaw;
        qint64 ts = 0;
        bool isRead = false, isFlagged = false, isAnswered = false, hasAttachments = false;
        bool sameAs(const Item &o) const;
    };

    explicit MessageModel(QObject *parent = nullptr);

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;
    bool canFetchMore(const QModelIndex &parent) const override;
    void fetchMore(const QModelIndex &parent) override;

    QString folderId() const { return m_folderId; }
    void setFolderId(const QString &id);
    QString accountId() const { return m_accountId; }
    void setAccountId(const QString &id);
    QString searchQuery() const { return m_query; }
    void setSearchQuery(const QString &q);
    bool unreadOnly() const { return m_unreadOnly; }
    void setUnreadOnly(bool on);
    bool loading() const { return m_loading; }
    int totalCount() const { return m_total; }
    int count() const { return int(m_items.size()); }

    Q_INVOKABLE void refresh();
    Q_INVOKABLE int indexOfMessage(const QString &id) const;
    Q_INVOKABLE QVariant get(int row) const;
    Q_INVOKABLE void setRead(int row, bool read);
    Q_INVOKABLE void toggleRead(int row);
    Q_INVOKABLE void toggleFlagged(int row);
    Q_INVOKABLE void archive(int row);

signals:
    void folderIdChanged();
    void accountIdChanged();
    void searchQueryChanged();
    void unreadOnlyChanged();
    void loadingChanged();
    void totalCountChanged();
    void countChanged();

private:
    static Item fromProto(const void *header);
    bool searchMode() const { return !m_query.isEmpty(); }
    void setLoading(bool l);
    void setTotal(int t);
    void load(bool append);
    void mergeReplace(std::vector<Item> fresh);
    void append(std::vector<Item> more);
    void setFlag(const QString &id, bool isReadChange, bool value);
    void emitRow(int row, const QList<int> &roles = {});

    QString m_folderId, m_accountId, m_query;
    std::vector<Item> m_items;
    int m_total = 0;
    bool m_unreadOnly = false;
    bool m_loading = false;
    int m_gen = 0;
    QTimer m_searchDebounce, m_eventDebounce;
};
