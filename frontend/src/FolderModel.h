#pragma once

#include <QAbstractListModel>
#include <QString>
#include <QTimer>
#include <QtQml/qqmlregistration.h>

#include <vector>

class FolderModel : public QAbstractListModel
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString accountId READ accountId WRITE setAccountId NOTIFY accountIdChanged FINAL)
    Q_PROPERTY(int count READ count NOTIFY countChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY loadingChanged FINAL)

public:
    enum Roles {
        FolderIdRole = Qt::UserRole + 1,
        NameRole,
        PathRole,
        UnreadCountRole,
        KindRole,
        SpecialUseRole,
        DisplayNameRole,
        IconRole,
    };

    struct Entry {
        QString id, name, path, kind, specialUse, displayName, icon;
        int unread = 0;
        int rank = 0;
        bool sameAs(const Entry &o) const;
    };

    explicit FolderModel(QObject *parent = nullptr);

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;

    QString accountId() const { return m_accountId; }
    void setAccountId(const QString &id);
    int count() const { return int(m_items.size()); }
    bool loading() const { return m_pending > 0; }

    Q_INVOKABLE void refresh();
    Q_INVOKABLE QString folderIdAt(int row) const;
    Q_INVOKABLE int indexOfFolder(const QString &folderId) const;

signals:
    void accountIdChanged();
    void countChanged();
    void loadingChanged();

private:
    void apply(std::vector<Entry> fresh);

    QString m_accountId;
    std::vector<Entry> m_items;
    int m_gen = 0;
    int m_pending = 0;
    QTimer m_debounce;
};
