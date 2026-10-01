// orders fixture postgresql v1. Static ORM declaration evidence only.
// Do not compile, migrate, run callbacks, or connect to a database.
package fixture

type User struct {
	TenantID  int64   `gorm:"column:tenant_id;type:BIGINT;not null;primaryKey;uniqueIndex:users_email_unique"`
	ID        int64   `gorm:"column:id;type:BIGINT;not null;primaryKey"`
	Email     string  `gorm:"column:email;type:TEXT;not null;uniqueIndex:users_email_unique"`
	DeletedAt *string `gorm:"column:deleted_at;type:timestamptz"`
}

type Order struct {
	TenantID   int64   `gorm:"column:tenant_id;type:BIGINT;not null;primaryKey"`
	ID         int64   `gorm:"column:id;type:BIGINT;not null;primaryKey"`
	UserID     *int64  `gorm:"column:user_id;type:BIGINT"`
	Status     string  `gorm:"column:status;type:TEXT;not null;default:'pending'"`
	Total      string  `gorm:"column:total;type:numeric(12,2);not null;default:0"`
	LegacyNote *string `gorm:"column:legacy_note;type:TEXT"`
	CreatedAt  string  `gorm:"column:created_at;type:timestamptz;not null;autoCreateTime"`
	User       User    `gorm:"foreignKey:TenantID,UserID;references:TenantID,ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

type OrderItem struct {
	TenantID int64  `gorm:"column:tenant_id;type:BIGINT;not null;primaryKey"`
	OrderID  int64  `gorm:"column:order_id;type:BIGINT;not null;primaryKey"`
	LineNo   int    `gorm:"column:line_no;type:INTEGER;not null;primaryKey"`
	SKU      string `gorm:"column:sku;type:TEXT;not null"`
	Quantity int    `gorm:"column:quantity;type:INTEGER;not null;default:1"`
	Order    Order  `gorm:"foreignKey:TenantID,OrderID;references:TenantID,ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

type Payment struct {
	TenantID   int64  `gorm:"column:tenant_id;type:BIGINT;not null;primaryKey;uniqueIndex:payments_order_unique"`
	ID         int64  `gorm:"column:id;type:BIGINT;not null;primaryKey"`
	OrderID    *int64 `gorm:"column:order_id;type:BIGINT;uniqueIndex:payments_order_unique"`
	Amount     string `gorm:"column:amount;type:numeric(12,2);not null"`
	ReceivedAt string `gorm:"column:received_at;type:timestamptz;not null;autoCreateTime"`
	Order      Order  `gorm:"foreignKey:TenantID,OrderID;references:TenantID,ID;constraint:OnUpdate:NO ACTION,OnDelete:NO ACTION"`
}

func (User) TableName() string      { return "public.users" }
func (Order) TableName() string     { return "public.orders" }
func (OrderItem) TableName() string { return "public.order_items" }
func (Payment) TableName() string   { return "public.payments" }
