package entities

type Merchant struct {
	ID            string
	Slug          string
	Name          string
	SchemaVersion uint
}

type Membership struct {
	MerchantID string
	Slug       string
	Name       string
	Role       string
}

type MerchantRequest struct {
	UserID   string // required, the owner
	Name     string // required, shown to customers
	Slug     string // required, used in public URLs, lowercase letters, digits and hyphens
	Currency string // optional ISO 4217 code, default VND
	Timezone string // optional IANA name, default Asia/Ho_Chi_Minh
}
