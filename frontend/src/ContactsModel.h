#pragma once

#include <QAbstractListModel>
#include <QString>
#include <QStringList>
#include <QTimer>
#include <QtQml/qqmlregistration.h>

#include <vector>

// Synced contacts matching `query`. With perEmail (composer autocomplete) each
// row is one email address of a contact and contacts without email are left out.
class ContactsModel : public QAbstractListModel
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString query READ query WRITE setQuery NOTIFY queryChanged FINAL)
    Q_PROPERTY(bool perEmail READ perEmail WRITE setPerEmail NOTIFY perEmailChanged FINAL)
    Q_PROPERTY(int limit READ limit WRITE setLimit NOTIFY limitChanged FINAL)
    // Composer mode: do not query until `query` has at least this many characters.
    Q_PROPERTY(int minQueryLength READ minQueryLength WRITE setMinQueryLength NOTIFY minQueryLengthChanged FINAL)
    Q_PROPERTY(int count READ count NOTIFY countChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY loadingChanged FINAL)

public:
    enum Roles {
        ContactIdRole = Qt::UserRole + 1,
        FullNameRole,
        InitialsRole,
        EmailRole,  // the row's address (perEmail) or the first one
        EmailsRole, // all addresses (QStringList)
        PhonesRole,
        OrganizationRole,
        AccountIdRole,
        AddressRole, // "Name <email>" ready for a To field
        ColorRole,   // stable avatar color
    };

    explicit ContactsModel(QObject *parent = nullptr);

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;

    QString query() const { return m_query; }
    void setQuery(const QString &q);
    bool perEmail() const { return m_perEmail; }
    void setPerEmail(bool v);
    int limit() const { return m_limit; }
    void setLimit(int v);
    int minQueryLength() const { return m_minQuery; }
    void setMinQueryLength(int v);
    int count() const { return int(m_rows.size()); }
    bool loading() const { return m_pending > 0; }

    Q_INVOKABLE QVariantMap get(int row) const;
    Q_INVOKABLE void refresh();

    // "Ana Horvat <ana@example.hr>"; quotes names that need it.
    static QString formatAddress(const QString &name, const QString &email);

signals:
    void queryChanged();
    void perEmailChanged();
    void limitChanged();
    void minQueryLengthChanged();
    void countChanged();
    void loadingChanged();

private:
    struct Row {
        QString id, name, email, org, accountId;
        QStringList emails, phones;
    };
    void apply(std::vector<Row> rows);

    QString m_query;
    bool m_perEmail = false;
    int m_limit = 1000;
    int m_minQuery = 0;
    int m_pending = 0;
    int m_gen = 0;
    std::vector<Row> m_rows;
    QTimer m_debounce;
};
