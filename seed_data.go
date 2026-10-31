package main

// The seeded company: Kestrel Labs, a ~50 person software company that has used Pocket ID since it was founded

const (
	domain         = "kestrel-labs.com"
	companyAgeDays = 420
	historyDays    = 80
)

type city struct {
	country  string
	ipPrefix string
	// utcOffset shifts the working hours of people in this city
	utcOffset int
}

var cities = map[string]city{
	"Zurich":     {"Switzerland", "178.197", 1},
	"Geneva":     {"Switzerland", "178.198", 1},
	"Berlin":     {"Germany", "91.64", 1},
	"London":     {"United Kingdom", "81.2", 0},
	"Lisbon":     {"Portugal", "85.240", 0},
	"Madrid":     {"Spain", "88.12", 1},
	"Amsterdam":  {"Netherlands", "84.241", 1},
	"Paris":      {"France", "90.112", 1},
	"Stockholm":  {"Sweden", "83.248", 1},
	"Copenhagen": {"Denmark", "87.49", 1},
	"Dublin":     {"Ireland", "86.40", 0},
	"Milan":      {"Italy", "93.45", 1},
	"New York":   {"United States", "72.229", -5},
	"Toronto":    {"Canada", "99.230", -5},
}

// offices maps the cities with an office to the office's public IP
var offices = map[string]string{"Zurich": "195.65.84.17", "Berlin": "217.110.42.9"}

var userAgents = map[string]string{
	"mac_chrome":    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36",
	"mac_safari":    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Safari/605.1.15",
	"mac_firefox":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:149.0) Gecko/20100101 Firefox/149.0",
	"mac_arc":       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36",
	"win_edge":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36 Edg/151.0.0.0",
	"win_chrome":    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36",
	"linux_firefox": "Mozilla/5.0 (X11; Linux x86_64; rv:149.0) Gecko/20100101 Firefox/149.0",
	"linux_chrome":  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36",
	"iphone":        "Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Mobile/15E148 Safari/604.1",
	"android":       "Mozilla/5.0 (Linux; Android 16; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Mobile Safari/537.36",
}

// deviceSets are the desktop and phone combinations people without explicit devices pick from
var deviceSets = [][2]string{
	{"mac_chrome", "iphone"}, {"mac_safari", "iphone"}, {"mac_firefox", "iphone"}, {"mac_chrome", "android"},
	{"mac_arc", "iphone"}, {"win_edge", "iphone"}, {"linux_firefox", "android"},
}

type authenticator struct {
	name       string
	aaguid     string
	transports []string
	synced     bool
}

var authenticators = map[string]authenticator{
	"icloud":     {"iCloud Keychain", "fbfc3007-154e-4ecc-8c0b-6e020557d7bd", []string{"hybrid", "internal"}, true},
	"google":     {"Google Password Manager", "ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4", []string{"hybrid", "internal"}, true},
	"1password":  {"1Password", "bada5566-a7aa-401f-bd96-45619a55120d", []string{"hybrid", "internal"}, true},
	"bitwarden":  {"Bitwarden", "d548826e-79b4-db40-a3d8-11116f7e8349", []string{"hybrid", "internal"}, true},
	"yubikey":    {"YubiKey 5C NFC", "d7781e5d-e353-46aa-afe2-3ca49f13332a", []string{"nfc", "usb"}, false},
	"windows":    {"Windows Hello", "08987058-cadc-4b81-b6e1-30de50dcbe96", []string{"internal"}, false},
	"chrome_mac": {"Chrome on Mac", "adce0002-35bc-c60a-648b-0b25f1f05503", []string{"internal"}, false},
}

type group struct {
	key  string
	name string
	// daysAgo dates groups that came later, zero spreads them over the company's first months
	daysAgo int
}

var groups = []group{
	{key: "admins", name: "Administrators"},
	{key: "leadership", name: "Leadership"},
	{key: "engineering", name: "Engineering"},
	{key: "backend", name: "Backend"},
	{key: "frontend", name: "Frontend"},
	{key: "platform", name: "Platform & SRE"},
	{key: "mobile", name: "Mobile"},
	{key: "qa", name: "Quality Assurance"},
	{key: "data", name: "Data & Analytics"},
	{key: "design", name: "Design"},
	{key: "product", name: "Product"},
	{key: "sales", name: "Sales"},
	{key: "marketing", name: "Marketing"},
	{key: "customer-success", name: "Customer Success"},
	{key: "people-ops", name: "People Ops"},
	{key: "finance", name: "Finance"},
	{key: "on-call", name: "On-Call Rotation", daysAgo: 270},
	{key: "contractors", name: "External Contractors", daysAgo: 36},
	{key: "office-zurich", name: "Office Zurich"},
	{key: "office-berlin", name: "Office Berlin", daysAgo: 210},
}

// groupClaims are custom claims set on whole groups
var groupClaims = map[string]map[string]string{
	"platform":    {"grafana_role": "Admin", "infra_access": "production"},
	"engineering": {"grafana_role": "Editor"},
	"data":        {"grafana_role": "Editor", "metabase_role": "analyst"},
	"leadership":  {"grafana_role": "Viewer", "metabase_role": "admin"},
	"contractors": {"contract_type": "external", "infra_access": "none"},
	"on-call":     {"pagerduty_schedule": "primary"},
}

type person struct {
	first, last string
	female      bool
	city        string
	title       string
	department  string
	groups      []string
	admin       bool
	locale      string
	// devices are the desktop and phone user agent keys, zero picks a random set
	devices [2]string
	// passkeys are authenticator keys, nil picks random ones
	passkeys []string
	// hiredDaysAgo dates recent hires, zero spreads people over the company's lifetime in list order
	hiredDaysAgo int
	// leftDaysAgo disables former employees, whose history stops on that day
	leftDaysAgo int
	// pending people were invited but never signed in, so they have no passkey and no history
	pending bool
}

var people = []person{
	{first: "Sophie", last: "Brunner", female: true, city: "Zurich", title: "Chief Executive Officer", department: "Leadership", groups: []string{"leadership"}},
	{first: "Julian", last: "Steiner", city: "Zurich", title: "Chief Technology Officer", department: "Leadership", groups: []string{"leadership", "engineering", "admins"}, admin: true, devices: [2]string{"mac_arc", "iphone"}, passkeys: []string{"1password", "yubikey"}},
	{first: "Marco", last: "Rossi", city: "Zurich", title: "Chief Financial Officer", department: "Finance", groups: []string{"leadership", "finance"}, locale: "it"},
	{first: "Lukas", last: "Meier", city: "Zurich", title: "Staff Backend Engineer", department: "Backend", groups: []string{"engineering", "backend", "on-call"}, locale: "de"},
	{first: "Priya", last: "Raman", female: true, city: "London", title: "Senior Backend Engineer", department: "Backend", groups: []string{"engineering", "backend", "on-call"}},
	{first: "Jonas", last: "Weber", city: "Berlin", title: "Backend Engineer", department: "Backend", groups: []string{"engineering", "backend"}, locale: "de", devices: [2]string{"linux_firefox", "android"}},
	{first: "Tomás", last: "Silva", city: "Lisbon", title: "Backend Engineer", department: "Backend", groups: []string{"engineering", "backend"}, locale: "pt"},
	{first: "Hannah", last: "Fischer", female: true, city: "Berlin", title: "Engineering Manager, Backend", department: "Backend", groups: []string{"engineering", "backend", "leadership"}, locale: "de"},
	{first: "Mateo", last: "García", city: "Madrid", title: "Backend Engineer", department: "Backend", groups: []string{"engineering", "backend"}, locale: "es"},
	{first: "Yuki", last: "Tanaka", female: true, city: "Amsterdam", title: "Senior Backend Engineer", department: "Backend", groups: []string{"engineering", "backend", "on-call"}},
	{first: "Daniel", last: "Okafor", city: "London", title: "Backend Engineer", department: "Backend", groups: []string{"engineering", "backend"}, hiredDaysAgo: 12},
	{first: "Lena", last: "Schmid", female: true, city: "Zurich", title: "Engineering Manager, Frontend", department: "Frontend", groups: []string{"engineering", "frontend", "leadership"}, locale: "de"},
	{first: "Noah", last: "Keller", city: "Zurich", title: "Senior Frontend Engineer", department: "Frontend", groups: []string{"engineering", "frontend"}, locale: "de", hiredDaysAgo: 58},
	{first: "Chloé", last: "Martin", female: true, city: "Paris", title: "Frontend Engineer", department: "Frontend", groups: []string{"engineering", "frontend"}, locale: "fr"},
	{first: "Arjun", last: "Mehta", city: "Berlin", title: "Frontend Engineer", department: "Frontend", groups: []string{"engineering", "frontend"}},
	{first: "Sara", last: "Lindqvist", female: true, city: "Stockholm", title: "Frontend Engineer", department: "Frontend", groups: []string{"engineering", "frontend"}, locale: "sv"},
	{first: "Felix", last: "Baumann", city: "Zurich", title: "Frontend Engineer", department: "Frontend", groups: []string{"engineering", "frontend"}, locale: "de"},
	{first: "Nico", last: "Huber", city: "Zurich", title: "Head of Platform", department: "Platform & SRE", groups: []string{"engineering", "platform", "on-call", "admins"}, admin: true, locale: "de", devices: [2]string{"linux_firefox", "iphone"}, passkeys: []string{"yubikey", "icloud"}},
	{first: "Aisha", last: "Khan", female: true, city: "London", title: "Site Reliability Engineer", department: "Platform & SRE", groups: []string{"engineering", "platform", "on-call"}, passkeys: []string{"yubikey", "1password"}},
	{first: "Ole", last: "Hansen", city: "Copenhagen", title: "Site Reliability Engineer", department: "Platform & SRE", groups: []string{"engineering", "platform", "on-call"}, locale: "da", devices: [2]string{"linux_chrome", "android"}},
	{first: "Mia", last: "Novak", female: true, city: "Zurich", title: "Platform Engineer", department: "Platform & SRE", groups: []string{"engineering", "platform"}, hiredDaysAgo: 19},
	{first: "Ruben", last: "de Vries", city: "Amsterdam", title: "Platform Engineer", department: "Platform & SRE", groups: []string{"engineering", "platform", "on-call"}, locale: "nl"},
	{first: "Elena", last: "Popescu", female: true, city: "Berlin", title: "Senior iOS Engineer", department: "Mobile", groups: []string{"engineering", "mobile"}},
	{first: "Kenji", last: "Watanabe", city: "Zurich", title: "Android Engineer", department: "Mobile", groups: []string{"engineering", "mobile"}, devices: [2]string{"mac_chrome", "android"}},
	{first: "Fatima", last: "Benali", female: true, city: "Zurich", title: "QA Lead", department: "Quality Assurance", groups: []string{"engineering", "qa"}, locale: "fr"},
	{first: "Patrick", last: "O'Brien", city: "Dublin", title: "QA Engineer", department: "Quality Assurance", groups: []string{"engineering", "qa"}, devices: [2]string{"win_chrome", "android"}},
	{first: "Inês", last: "Duarte", female: true, city: "Lisbon", title: "Head of Data", department: "Data & Analytics", groups: []string{"data", "leadership"}, locale: "pt"},
	{first: "Samuel", last: "Kowalski", city: "Berlin", title: "Data Engineer", department: "Data & Analytics", groups: []string{"data", "engineering"}, locale: "pl"},
	{first: "Laura", last: "Bianchi", female: true, city: "Zurich", title: "Data Analyst", department: "Data & Analytics", groups: []string{"data"}, locale: "it", devices: [2]string{"win_edge", "iphone"}},
	{first: "Emma", last: "Lehmann", female: true, city: "Zurich", title: "Head of Design", department: "Design", groups: []string{"design", "leadership"}, locale: "de"},
	{first: "Oscar", last: "Nilsson", city: "Stockholm", title: "Senior Product Designer", department: "Design", groups: []string{"design"}},
	{first: "Zoe", last: "Carter", female: true, city: "New York", title: "Product Designer", department: "Design", groups: []string{"design"}},
	{first: "Anna", last: "Vogel", female: true, city: "Zurich", title: "Head of Product", department: "Product", groups: []string{"product", "leadership"}, locale: "de"},
	{first: "Ben", last: "Thompson", city: "London", title: "Senior Product Manager", department: "Product", groups: []string{"product"}},
	{first: "Clara", last: "Wagner", female: true, city: "Berlin", title: "Product Manager", department: "Product", groups: []string{"product"}, locale: "de", hiredDaysAgo: 26},
	{first: "Michael", last: "Brown", city: "New York", title: "VP Sales", department: "Sales", groups: []string{"sales", "leadership"}, devices: [2]string{"win_edge", "iphone"}},
	{first: "Jessica", last: "Lee", female: true, city: "Toronto", title: "Account Executive", department: "Sales", groups: []string{"sales"}, devices: [2]string{"win_chrome", "iphone"}},
	{first: "Thomas", last: "Müller", city: "Zurich", title: "Account Executive DACH", department: "Sales", groups: []string{"sales"}, locale: "de", devices: [2]string{"win_edge", "android"}},
	{first: "Giulia", last: "Ferrari", female: true, city: "Milan", title: "Account Executive", department: "Sales", groups: []string{"sales"}, locale: "it"},
	{first: "Olivia", last: "Hughes", female: true, city: "London", title: "Head of Marketing", department: "Marketing", groups: []string{"marketing", "leadership"}},
	{first: "Max", last: "Zimmermann", city: "Berlin", title: "Growth Marketer", department: "Marketing", groups: []string{"marketing"}, locale: "de"},
	{first: "Camille", last: "Dubois", female: true, city: "Geneva", title: "Content Marketing Manager", department: "Marketing", groups: []string{"marketing"}, locale: "fr", hiredDaysAgo: 47},
	{first: "Ryan", last: "Murphy", city: "Dublin", title: "Head of Customer Success", department: "Customer Success", groups: []string{"customer-success", "leadership"}},
	{first: "Nora", last: "Ammann", female: true, city: "Zurich", title: "Customer Success Manager", department: "Customer Success", groups: []string{"customer-success"}, locale: "de"},
	{first: "Carlos", last: "Mendes", city: "Lisbon", title: "Support Engineer", department: "Customer Success", groups: []string{"customer-success"}, locale: "pt"},
	{first: "Alice", last: "Laurent", female: true, city: "Geneva", title: "Support Engineer", department: "Customer Success", groups: []string{"customer-success"}, locale: "fr", devices: [2]string{"win_chrome", "android"}},
	{first: "Sandra", last: "Frei", female: true, city: "Zurich", title: "Head of People", department: "People Ops", groups: []string{"people-ops", "leadership"}, locale: "de"},
	{first: "Tim", last: "Hofmann", city: "Zurich", title: "People Partner", department: "People Ops", groups: []string{"people-ops"}, locale: "de"},
	{first: "Katharina", last: "Berger", female: true, city: "Zurich", title: "Accountant", department: "Finance", groups: []string{"finance"}, locale: "de", devices: [2]string{"win_edge", "iphone"}},
	{first: "Stefan", last: "Kunz", city: "Zurich", title: "IT Administrator", department: "Platform & SRE", groups: []string{"admins", "platform"}, admin: true, locale: "de", passkeys: []string{"yubikey", "bitwarden"}},
	{first: "Pedro", last: "Alves", city: "Lisbon", title: "Contract Backend Engineer", department: "External Contractors", groups: []string{"contractors", "backend"}, locale: "pt", hiredDaysAgo: 34},
	{first: "Kevin", last: "Schwarz", city: "Berlin", title: "Frontend Engineer", department: "Frontend", groups: []string{"engineering", "frontend"}, hiredDaysAgo: 290, leftDaysAgo: 41},
	{first: "Maria", last: "Santos", female: true, city: "Lisbon", title: "Account Executive", department: "Sales", groups: []string{"sales"}, hiredDaysAgo: 240, leftDaysAgo: 67},
	{first: "Liam", last: "Walsh", city: "Dublin", title: "Contract QA Engineer", department: "External Contractors", groups: []string{"contractors", "qa"}, hiredDaysAgo: 2, pending: true},
	{first: "Eva", last: "Horvat", female: true, city: "Zurich", title: "Product Designer", department: "Design", groups: []string{"design"}, hiredDaysAgo: 1, pending: true},
}

type app struct {
	key, name, description string
	// preset is the logo preset reference, empty for the company's own apps
	preset string
	// host is the subdomain of the app, empty for apps without a web UI
	host string
	// groups restricts the app to members of these groups, nil means everyone
	groups []string
	// usage is the chance that someone uses the app in a sign-in session
	usage float64
	// createdDaysAgo dates apps added recently, zero picks a date in the company's first year
	createdDaysAgo int
	callbackURL    string
	launchURL      string
	policy         string
	skipConsent    bool
	reauth         bool
	public         bool
	pkce           bool
	deviceCode     bool
	groupsScope    bool
	offlineAccess  bool
}

var apps = []app{
	{key: "mattermost", preset: "mattermost", name: "Mattermost", description: "Team chat and incident channels", host: "chat", usage: 0.85, skipConsent: true, offlineAccess: true},
	{key: "nextcloud", preset: "nextcloud", name: "Nextcloud", description: "Files, calendars and contacts", host: "cloud", usage: 0.55, skipConsent: true, groupsScope: true, offlineAccess: true},
	{key: "outline", preset: "outline", name: "Outline", description: "Company wiki and handbook", host: "wiki", usage: 0.45, skipConsent: true, groupsScope: true},
	{key: "kimai", preset: "kimai", name: "Kimai", description: "Time tracking", host: "time", usage: 0.15},
	{key: "vaultwarden", preset: "vaultwarden", name: "Vaultwarden", description: "Shared password vault", host: "vault", usage: 0.2, reauth: true},
	{key: "open-webui", preset: "open-webui", name: "Open WebUI", description: "Internal AI assistant", host: "ai", usage: 0.35, createdDaysAgo: 23},
	{key: "home-assistant", preset: "home-assistant", name: "Home Assistant", description: "Office climate, lights and door access", host: "home", groups: []string{"office-zurich", "admins"}, usage: 0.08, groupsScope: true},
	{key: "forgejo", preset: "forgejo", name: "Forgejo", description: "Git hosting and code review", host: "git", groups: []string{"engineering", "design", "product", "data", "contractors"}, usage: 0.8, groupsScope: true},
	{key: "woodpecker", preset: "woodpecker-ci", name: "Woodpecker CI", description: "Continuous integration pipelines", host: "ci", groups: []string{"engineering", "contractors"}, usage: 0.35},
	{key: "grafana", preset: "grafana", name: "Grafana", description: "Dashboards, metrics and alerting", host: "grafana", groups: []string{"engineering", "data", "leadership"}, usage: 0.35, policy: "grafana", groupsScope: true},
	{key: "sentry", preset: "sentry", name: "Sentry", description: "Error tracking", host: "sentry", groups: []string{"engineering", "product"}, usage: 0.3},
	{key: "argocd", preset: "argo-cd", name: "Argo CD", description: "GitOps deployments for the production clusters", host: "argocd", groups: []string{"platform", "backend"}, usage: 0.3, reauth: true, groupsScope: true},
	{key: "harbor", preset: "harbor", name: "Harbor", description: "Container registry", host: "registry", groups: []string{"engineering"}, usage: 0.1, groupsScope: true},
	{key: "proxmox", preset: "proxmox", name: "Proxmox VE", description: "On-prem virtualization cluster", host: "pve", groups: []string{"platform", "admins"}, usage: 0.25, reauth: true, groupsScope: true},
	{key: "portainer", preset: "portainer", name: "Portainer", description: "Docker hosts in the office rack", host: "portainer", groups: []string{"platform"}, usage: 0.1, groupsScope: true},
	{key: "minio", preset: "minio", name: "MinIO", description: "Object storage console", host: "s3", groups: []string{"platform", "data"}, usage: 0.12, groupsScope: true},
	{key: "coder", preset: "coder", name: "Coder", description: "Cloud development environments", host: "coder", groups: []string{"engineering", "contractors"}, usage: 0.4, createdDaysAgo: 44, groupsScope: true},
	{key: "metabase", preset: "metabase", name: "Metabase", description: "Business intelligence and KPIs", host: "bi", groups: []string{"data", "leadership", "product", "finance", "sales"}, usage: 0.3, groupsScope: true},
	{key: "penpot", preset: "penpot", name: "Penpot", description: "Design files and prototypes", host: "design", groups: []string{"design", "product", "frontend"}, usage: 0.45},
	{key: "plane", preset: "plane", name: "Plane", description: "Issue tracking and roadmaps", host: "plane", groups: []string{"engineering", "design", "product", "customer-success", "contractors"}, usage: 0.6},
	{key: "zammad", preset: "zammad", name: "Zammad", description: "Customer support helpdesk", host: "support", groups: []string{"customer-success", "product", "qa"}, usage: 0.5},
	{key: "twenty", preset: "twenty-crm", name: "Twenty CRM", description: "Customers, deals and pipeline", host: "crm", groups: []string{"sales", "marketing", "customer-success", "leadership"}, usage: 0.6},
	{key: "n8n", preset: "n8n", name: "n8n", description: "Workflow automation", host: "automation", groups: []string{"platform", "marketing", "people-ops"}, usage: 0.15},
	{key: "paperless", preset: "paperless-ngx", name: "Paperless-ngx", description: "Contracts, invoices and receipts", host: "docs", groups: []string{"finance", "people-ops", "leadership"}, usage: 0.3},
	{key: "tailscale", preset: "tailscale", name: "Tailscale", description: "VPN to the office and infrastructure", groups: []string{"engineering", "admins", "contractors"}, usage: 0.15, policy: "minimal", callbackURL: "https://login.tailscale.com/a/oauth_response", launchURL: "https://login.tailscale.com"},
	{key: "admin-console", name: "Kestrel Admin Console", description: "Internal back office for customer accounts", host: "admin", groups: []string{"admins", "customer-success", "leadership", "backend"}, usage: 0.35},
	{key: "cli", name: "Kestrel CLI", description: "Command line tool for the Kestrel platform", groups: []string{"engineering", "contractors"}, usage: 0.1, public: true, deviceCode: true, offlineAccess: true},
	{key: "staging", name: "Staging Dashboard", description: "Pre-production environment of the Kestrel app", host: "staging", groups: []string{"engineering", "product", "qa", "design"}, usage: 0.25, pkce: true},
}

type permission struct{ key, name, description string }

// grant lists the permissions a client gets on behalf of users and on its own
type grant struct{ delegated, client []string }

type resourceAPI struct {
	name, resource string
	permissions    []permission
	grants         map[string]grant
}

var resourceAPIs = []resourceAPI{
	{
		name:     "Kestrel Platform API",
		resource: "https://api." + domain,
		permissions: []permission{
			{"projects:read", "Read projects", "List and inspect customer projects"},
			{"projects:write", "Manage projects", "Create, update and archive projects"},
			{"deployments:read", "Read deployments", "View deployment history and status"},
			{"deployments:write", "Trigger deployments", "Start, roll back and cancel deployments"},
			{"accounts:read", "Read accounts", "View customer accounts and their plans"},
			{"accounts:write", "Manage accounts", "Change plans, quotas and account status"},
			{"admin", "Full admin access", "Unrestricted access to every endpoint"},
		},
		grants: map[string]grant{
			"cli":           {delegated: []string{"projects:read", "projects:write", "deployments:read", "deployments:write"}},
			"admin-console": {delegated: []string{"accounts:read", "accounts:write", "projects:read"}},
			"staging":       {delegated: []string{"projects:read", "deployments:read"}},
		},
	},
	{
		name:     "Billing Service",
		resource: "https://billing.internal." + domain,
		permissions: []permission{
			{"invoices:read", "Read invoices", ""},
			{"invoices:write", "Issue invoices", "Create and void invoices"},
			{"customers:read", "Read customers", ""},
			{"refunds:create", "Create refunds", "Refund a customer payment"},
		},
		grants: map[string]grant{
			"admin-console": {delegated: []string{"invoices:read", "customers:read", "refunds:create"}},
			"n8n":           {client: []string{"invoices:read", "customers:read"}},
			"metabase":      {client: []string{"invoices:read"}},
		},
	},
	{
		name:     "Metrics Ingest",
		resource: "https://metrics." + domain,
		permissions: []permission{
			{"metrics:write", "Write metrics", "Push samples to the ingest endpoint"},
			{"metrics:read", "Query metrics", ""},
		},
		grants: map[string]grant{
			"grafana":    {delegated: []string{"metrics:read"}},
			"woodpecker": {client: []string{"metrics:write"}},
		},
	},
}
