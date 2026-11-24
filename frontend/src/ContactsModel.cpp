#include "ContactsModel.h"

#include "Rpc.h"
#include "mailand.grpc.pb.h"

#include <QColor>
#include <QDateTime>
#include <QLocale>
#include <QRegularExpression>

namespace {

QString s(const std::string &v) { return QString::fromStdString(v); }

QString initials(const QString &name)
{
    QString out;
    for (const QString &part : name.split(QRegularExpression(QStringLiteral("[\\s.@_-]+")), Qt::SkipEmptyParts)) {
        if (!part[0].isLetterOrNumber())
            continue;
        out += part[0].toUpper();
        if (out.size() == 2)
            break;
    }
    return out.isEmpty() ? QStringLiteral("?") : out;
}

QString avatarColor(const QString &key)
{
    // Muted hues that keep white initials readable on the dark theme.
    const int hue = int(qHash(key) % 360);
    return QColor::fromHsl(hue, 90, 92).name();
}

} // namespace

ContactsModel::ContactsModel(QObject *parent) : QAbstractListModel(parent)
{
    m_debounce.setSingleShot(true);
    m_debounce.setInterval(120);
    connect(&m_debounce, &QTimer::timeout, this, &ContactsModel::refresh);
    connect(Rpc::instance(), &Rpc::daemonEvent, this, [this](int type, const QString &, const QString &, const QString &) {
        if (type == mailand::v1::DaemonEvent::CONTACTS_UPDATED && !m_debounce.isActive())
            m_debounce.start();
    });
    connect(Rpc::instance(), &Rpc::connectedChanged, this, [this] {
        if (Rpc::instance()->connected())
            m_debounce.start();
    });
    m_debounce.start(); // after QML has set the properties
}

int ContactsModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : int(m_rows.size());
}

QString ContactsModel::formatAddress(const QString &name, const QString &email)
{
    if (name.isEmpty() || name == email)
        return email;
    static const QRegularExpression special(QStringLiteral("[,;<>@\"()\\[\\]:]"));
    QString n = name;
    if (n.contains(special))
        n = QLatin1Char('"') + QString(n).replace(QLatin1Char('"'), QLatin1Char('\'')) + QLatin1Char('"');
    return n + QStringLiteral(" <") + email + QLatin1Char('>');
}

QVariant ContactsModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= int(m_rows.size()))
        return {};
    const Row &r = m_rows[index.row()];
    switch (role) {
    case ContactIdRole: return r.id;
    case FullNameRole: return r.name;
    case InitialsRole: return initials(r.name);
    case EmailRole: return r.email;
    case EmailsRole: return r.emails;
    case PhonesRole: return r.phones;
    case OrganizationRole: return r.org;
    case AccountIdRole: return r.accountId;
    case AddressRole: return r.email.isEmpty() ? QString() : formatAddress(r.name, r.email);
    case ColorRole: return avatarColor(r.id + r.name);
    case ReadOnlyRole: return r.readOnly;
    default: return {};
    }
}

QHash<int, QByteArray> ContactsModel::roleNames() const
{
    return {
        {ContactIdRole, "contactId"}, {FullNameRole, "fullName"}, {InitialsRole, "initials"},
        {EmailRole, "email"}, {EmailsRole, "emails"}, {PhonesRole, "phones"},
        {OrganizationRole, "organization"}, {AccountIdRole, "accountId"}, {AddressRole, "address"},
        {ColorRole, "avatarColor"}, {ReadOnlyRole, "readOnly"},
    };
}

QVariantMap ContactsModel::get(int row) const
{
    QVariantMap out;
    if (row < 0 || row >= int(m_rows.size()))
        return out;
    const QHash<int, QByteArray> names = roleNames();
    for (auto it = names.cbegin(); it != names.cend(); ++it)
        out.insert(QString::fromLatin1(it.value()), data(index(row), it.key()));
    return out;
}

void ContactsModel::setQuery(const QString &q)
{
    if (q == m_query)
        return;
    m_query = q;
    emit queryChanged();
    m_debounce.start();
}

void ContactsModel::setPerEmail(bool v)
{
    if (v == m_perEmail)
        return;
    m_perEmail = v;
    emit perEmailChanged();
    m_debounce.start();
}

void ContactsModel::setLimit(int v)
{
    if (v == m_limit)
        return;
    m_limit = v;
    emit limitChanged();
    m_debounce.start();
}

void ContactsModel::setMinQueryLength(int v)
{
    if (v == m_minQuery)
        return;
    m_minQuery = v;
    emit minQueryLengthChanged();
    m_debounce.start();
}

void ContactsModel::refresh()
{
    const QString q = m_query.trimmed();
    const int gen = ++m_gen;
    if (q.size() < m_minQuery) {
        apply({});
        return;
    }
    if (m_pending++ == 0)
        emit loadingChanged();
    const bool perEmail = m_perEmail;
    const int limit = m_limit;
    Rpc::instance()->listContacts(
        q, quint32(limit), perEmail, this,
        [this, gen, perEmail, limit, q](const grpc::Status &st, const mailand::v1::ContactList &resp) {
            if (--m_pending == 0)
                emit loadingChanged();
            if (gen != m_gen || !st.ok())
                return;
            const QString lq = q.toLower();
            std::vector<Row> rows;
            for (const auto &c : resp.contacts()) {
                Row base{s(c.id()), s(c.full_name()), QString(), s(c.organization()), s(c.account_id()), {}, {},
                         c.read_only()};
                for (const auto &e : c.emails())
                    base.emails << s(e);
                for (const auto &p : c.phones())
                    base.phones << s(p);
                if (!perEmail) {
                    base.email = base.emails.value(0);
                    rows.push_back(std::move(base));
                    continue;
                }
                // One row per address; if the query names an address, offer only matching ones
                // unless the name itself matched.
                const bool nameHit = base.name.toLower().contains(lq) || base.org.toLower().contains(lq);
                for (const QString &e : std::as_const(base.emails)) {
                    if (!nameHit && !e.toLower().contains(lq))
                        continue;
                    Row r = base;
                    r.email = e;
                    rows.push_back(std::move(r));
                }
                if (int(rows.size()) >= limit)
                    break;
            }
            apply(std::move(rows));
        });
}

void ContactsModel::apply(std::vector<Row> rows)
{
    const int before = int(m_rows.size());
    beginResetModel();
    m_rows = std::move(rows);
    endResetModel();
    if (before != int(m_rows.size()))
        emit countChanged();
}

int ContactsModel::indexOf(const QString &contactId) const
{
    for (size_t i = 0; i < m_rows.size(); ++i)
        if (m_rows[i].id == contactId)
            return int(i);
    return -1;
}

void ContactsModel::setSaving(bool v)
{
    if (v == m_saving)
        return;
    m_saving = v;
    emit savingChanged();
}

void ContactsModel::save(const QVariantMap &f)
{
    mailand::v1::SaveContactRequest req;
    req.set_contact_id(f.value(QStringLiteral("contactId")).toString().toStdString());
    req.set_account_id(f.value(QStringLiteral("accountId")).toString().toStdString());
    req.set_full_name(f.value(QStringLiteral("fullName")).toString().trimmed().toStdString());
    req.set_organization(f.value(QStringLiteral("organization")).toString().trimmed().toStdString());
    for (const QString &e : f.value(QStringLiteral("emails")).toStringList())
        if (!e.trimmed().isEmpty())
            req.add_emails(e.trimmed().toStdString());
    for (const QString &p : f.value(QStringLiteral("phones")).toStringList())
        if (!p.trimmed().isEmpty())
            req.add_phones(p.trimmed().toStdString());
    setSaving(true);
    Rpc::instance()->saveContact(std::move(req), this, [this](const grpc::Status &st, const mailand::v1::Contact &c) {
        setSaving(false);
        if (!st.ok()) {
            emit editFailed(Rpc::statusText(st));
            return;
        }
        const QString id = s(c.id());
        refresh();
        emit contactSaved(id);
    });
}

void ContactsModel::remove(const QString &contactId)
{
    setSaving(true);
    Rpc::instance()->deleteContact(contactId, this, [this, contactId](const grpc::Status &st, const mailand::v1::Empty &) {
        setSaving(false);
        if (!st.ok()) {
            emit editFailed(Rpc::statusText(st));
            return;
        }
        refresh();
        emit contactDeleted(contactId);
    });
}

void ContactsModel::loadRecentMail(const QStringList &emails)
{
    const int gen = ++m_recentGen;
    if (emails.isEmpty()) {
        m_recent.clear();
        emit recentMailChanged();
        return;
    }
    Rpc::instance()->listMessagesByAddress(
        emails, 8, this, [this, gen](const grpc::Status &st, const mailand::v1::MessageHeaderList &resp) {
            if (gen != m_recentGen)
                return;
            QVariantList out;
            if (st.ok()) {
                const QLocale loc;
                for (const auto &m : resp.messages()) {
                    const QDateTime ts = QDateTime::fromSecsSinceEpoch(m.timestamp_unix());
                    const QString name = s(m.from_name());
                    out.append(QVariantMap{{QStringLiteral("id"), s(m.id())},
                                           {QStringLiteral("subject"), m.subject().empty() ? QStringLiteral("(no subject)")
                                                                                           : s(m.subject())},
                                           {QStringLiteral("sender"), name.isEmpty() ? s(m.from_address()) : name},
                                           {QStringLiteral("dateText"), loc.toString(ts.date(), QLocale::ShortFormat)},
                                           {QStringLiteral("isRead"), m.is_read()}});
                }
            }
            m_recent = out;
            emit recentMailChanged();
        });
}
