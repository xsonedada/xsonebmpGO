package database

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

var DB *sql.DB

func InitDB() {
	dbPassword := os.Getenv("DB_PASSWORD")
	if dbPassword == "" {
		log.Fatal("обязательная переменная окружения не задана: DB_PASSWORD")
	}
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_PORT", "5432"),
		getEnv("DB_USER", "postgres"),
		dbPassword,
		getEnv("DB_NAME", "xsonebmp"),
		getEnv("DB_SSLMODE", "disable"),
	)

	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Настройка пула соединений
	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(10)
	DB.SetConnMaxLifetime(5 * time.Minute)
	DB.SetConnMaxIdleTime(1 * time.Minute)

	if err = DB.Ping(); err != nil {
		log.Fatal("Failed to ping database:", err)
	}

	createTables()
	migrateColumns()
	createIndexes()
	insertDefaultData()
	startCleanupRoutine()

	log.Println("✅ Database connected successfully")
}
func createTables() {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
            id SERIAL PRIMARY KEY,
            username VARCHAR(100) UNIQUE NOT NULL,
            email VARCHAR(200) UNIQUE NOT NULL,
            password TEXT NOT NULL,
            balance NUMERIC(10,2) DEFAULT 10000.00,
            level INTEGER DEFAULT 1,
            orders INTEGER DEFAULT 0,
            rating NUMERIC(3,2) DEFAULT 5.00,
            reviews INTEGER DEFAULT 0,
            referral_code VARCHAR(20) UNIQUE,
            created_at TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS boosts (
    		id SERIAL PRIMARY KEY,
    		game VARCHAR(100) NOT NULL,
    		title VARCHAR(300) NOT NULL,
    		description TEXT DEFAULT '',
    		price NUMERIC(10,2) NOT NULL,
    		rating NUMERIC(3,2) DEFAULT 5.00,
    		reviews INTEGER DEFAULT 0,
    		user_id INTEGER DEFAULT 0,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS banned_users (
    		user_id INTEGER PRIMARY KEY,
    		reason TEXT DEFAULT '',
    		banned_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS transactions (
   			 id SERIAL PRIMARY KEY,
   			 user_id INTEGER,
   			 type VARCHAR(50),
   			 amount DECIMAL(10,2),
   			 description TEXT,
   			 created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS push_subscriptions (
        id SERIAL PRIMARY KEY,
        user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
        endpoint TEXT NOT NULL,
        auth TEXT NOT NULL,
        p256dh TEXT NOT NULL,
        user_agent TEXT,
        created_at TIMESTAMP DEFAULT NOW(),
        updated_at TIMESTAMP DEFAULT NOW(),
        UNIQUE(user_id, endpoint)
    	)`,

		`CREATE TABLE IF NOT EXISTS referrals (
    	id SERIAL PRIMARY KEY,
    	referrer_id INTEGER REFERENCES users(id),
    	referred_id INTEGER REFERENCES users(id),
    	code TEXT UNIQUE NOT NULL,
    	level INTEGER DEFAULT 1,
    	earnings DECIMAL(10,2) DEFAULT 0,
    	created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS referral_earnings (
    	id SERIAL PRIMARY KEY,
    	user_id INTEGER REFERENCES users(id),
    	order_id INTEGER REFERENCES orders(id),
    	from_user_id INTEGER REFERENCES users(id),
    	level INTEGER,
    	percent DECIMAL(5,2),
    	amount DECIMAL(10,2),
    	created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS user_sessions (
    	id SERIAL PRIMARY KEY,
    	user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    	session_token TEXT UNIQUE NOT NULL,
    	ip TEXT,
    	user_agent TEXT,
    	location TEXT,
    	device TEXT DEFAULT '',
    	is_active BOOLEAN DEFAULT true,
    	revoked BOOLEAN DEFAULT false,
    	created_at TIMESTAMP DEFAULT NOW(),
    	last_seen TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS qr_sessions (
        id SERIAL PRIMARY KEY,
        token TEXT UNIQUE NOT NULL,
        session_id TEXT,
        ip TEXT,
        user_agent TEXT,
        user_id INTEGER REFERENCES users(id),
        confirmed BOOLEAN DEFAULT false,
        approved BOOLEAN DEFAULT false,
        scanned BOOLEAN DEFAULT false,
        created_at TIMESTAMP DEFAULT NOW(),
        confirmed_at TIMESTAMP,
        expires_at TIMESTAMP DEFAULT NOW() + INTERVAL '5 minutes'
    	)`,

		`CREATE TABLE IF NOT EXISTS password_resets (
    		id SERIAL PRIMARY KEY,
    		user_id INTEGER NOT NULL,
    		code VARCHAR(6) NOT NULL,
    		expires_at TIMESTAMP NOT NULL,
    		used BOOLEAN DEFAULT false,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS settings (
    		key VARCHAR(100) PRIMARY KEY,
    		value TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS featured_boosts (
    		boost_id INTEGER PRIMARY KEY,
    		featured_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS verified_sellers (
    		user_id INTEGER PRIMARY KEY,
    		verified_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS admin_logs (
   			id SERIAL PRIMARY KEY,
   			admin_id INTEGER,
   			action TEXT,
   			details TEXT,
   			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS seller_levels (
    		level INTEGER PRIMARY KEY,
    		name VARCHAR(50) NOT NULL,
    		min_orders INTEGER NOT NULL,
    		min_rating DECIMAL(3,2) NOT NULL,
    		icon VARCHAR(10) NOT NULL,
    		color VARCHAR(20) NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS achievements (
    		id SERIAL PRIMARY KEY,
    		name VARCHAR(100) NOT NULL,
    		description TEXT,
    		icon VARCHAR(10) NOT NULL,
    		condition_field VARCHAR(50) NOT NULL,
    		condition_value INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS user_achievements (
    		user_id INTEGER,
    		achievement_id INTEGER,
    		achieved_at TIMESTAMP DEFAULT NOW(),
    		PRIMARY KEY (user_id, achievement_id)
		)`,

		`CREATE TABLE IF NOT EXISTS cart_items (
            id SERIAL PRIMARY KEY,
            user_id INTEGER NOT NULL,
            boost_id INTEGER NOT NULL,
            title VARCHAR(300) DEFAULT '',
            price NUMERIC(10,2) DEFAULT 0,
            game VARCHAR(100) DEFAULT '',
            booster VARCHAR(100) DEFAULT '',
            booster_id INTEGER DEFAULT 0,
            quantity INTEGER DEFAULT 1
        )`,

		`CREATE TABLE IF NOT EXISTS disputes (
    		id SERIAL PRIMARY KEY,
    		order_id INTEGER UNIQUE,
    		user_id INTEGER,
    		reason TEXT NOT NULL,
    		description TEXT,
    		status VARCHAR(50) DEFAULT 'open',
    		admin_decision TEXT,
    		created_at TIMESTAMP DEFAULT NOW(),
    		resolved_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS dispute_messages (
    		id SERIAL PRIMARY KEY,
    		dispute_id INTEGER,
    		user_id INTEGER,
    		username VARCHAR(100),
    		message TEXT,
    		is_admin BOOLEAN DEFAULT false,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS review_likes (
    		review_id INTEGER,
    		user_id INTEGER,
    		created_at TIMESTAMP DEFAULT NOW(),
    		PRIMARY KEY (review_id, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS promocodes (
    		id SERIAL PRIMARY KEY,
    		code VARCHAR(50) UNIQUE NOT NULL,
    		discount_percent INTEGER NOT NULL,
    		max_uses INTEGER DEFAULT 0,
    		used_count INTEGER DEFAULT 0,
    		min_order_amount DECIMAL(10,2) DEFAULT 0,
    		is_active BOOLEAN DEFAULT true,
    		expires_at TIMESTAMP,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS tickets (
    		id SERIAL PRIMARY KEY,
    		user_id INTEGER NOT NULL,
    		category VARCHAR(50) NOT NULL,
    		subject VARCHAR(300) NOT NULL,
    		description TEXT NOT NULL,
    		status VARCHAR(50) DEFAULT 'open',
    		priority VARCHAR(20) DEFAULT 'normal',
    		admin_id INTEGER,
    		created_at TIMESTAMP DEFAULT NOW(),
    		updated_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS ticket_messages (
    		id SERIAL PRIMARY KEY,
    		ticket_id INTEGER NOT NULL,
    		user_id INTEGER NOT NULL,
    		username VARCHAR(100) NOT NULL,
    		message TEXT NOT NULL,
    		is_admin BOOLEAN DEFAULT false,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS escrow_transactions (
    		id SERIAL PRIMARY KEY,
    		order_id INTEGER UNIQUE NOT NULL,
    		buyer_id INTEGER NOT NULL,
    		seller_id INTEGER NOT NULL,
    		amount DECIMAL(10,2) NOT NULL,
    		status VARCHAR(50) DEFAULT 'frozen',
    		frozen_at TIMESTAMP DEFAULT NOW(),
    		released_at TIMESTAMP,
    		refunded_at TIMESTAMP,
    		transaction_hash VARCHAR(100)
		)`,

		`CREATE TABLE IF NOT EXISTS transaction_history (
    		id SERIAL PRIMARY KEY,
    		user_id INTEGER NOT NULL,
    		type VARCHAR(50) NOT NULL,
    		amount DECIMAL(10,2) NOT NULL,
    		balance_before DECIMAL(10,2),
    		balance_after DECIMAL(10,2),
    		description TEXT,
    		order_id INTEGER,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS platform_fees (
    		id SERIAL PRIMARY KEY,
    		order_id INTEGER UNIQUE,
    		amount DECIMAL(10,2) NOT NULL,
    		fee_percent DECIMAL(5,2) DEFAULT 5.00,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS used_promocodes (
    		id SERIAL PRIMARY KEY,
    		user_id INTEGER,
    		promocode_id INTEGER,
    		order_id INTEGER,
    		discount_amount DECIMAL(10,2),
    		used_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS notifications (
    		id SERIAL PRIMARY KEY,
    		user_id INTEGER NOT NULL,
    		text TEXT NOT NULL,
    		link VARCHAR(500) DEFAULT '',
    		is_read BOOLEAN DEFAULT false,
    		created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS orders (
            id SERIAL PRIMARY KEY,
            user_id INTEGER NOT NULL,
            boost_id INTEGER DEFAULT 0,
            booster_id INTEGER DEFAULT 0,
            title VARCHAR(300) DEFAULT '',
            game VARCHAR(100) DEFAULT '',
            booster VARCHAR(100) DEFAULT '',
            total NUMERIC(10,2) NOT NULL,
            status VARCHAR(50) DEFAULT 'processing',
            rated BOOLEAN DEFAULT false,
            created_at TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS messages (
            id SERIAL PRIMARY KEY,
            order_id INTEGER NOT NULL,
            user_id INTEGER NOT NULL,
            username VARCHAR(100) DEFAULT '',
            text TEXT DEFAULT '',
            created_at TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS reviews (
            id SERIAL PRIMARY KEY,
            order_id INTEGER NOT NULL,
            user_id INTEGER NOT NULL,
            booster_id INTEGER DEFAULT 0,
            username VARCHAR(100) DEFAULT '',
            rating INTEGER DEFAULT 5,
            text TEXT DEFAULT '',
            created_at TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS user_online (
    		user_id INTEGER PRIMARY KEY,
    		last_seen TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS sales (
   		 	id SERIAL PRIMARY KEY,
   		 	name VARCHAR(200) NOT NULL,
   		 	discount_percent INTEGER NOT NULL,
   		 	start_date TIMESTAMP NOT NULL,
   		 	end_date TIMESTAMP NOT NULL,
   		 	is_active BOOLEAN DEFAULT true,
   		 	created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS user_badges (
    		id SERIAL PRIMARY KEY,
    		user_id INTEGER NOT NULL,
    		badge_type VARCHAR(50) NOT NULL,
    		badge_name VARCHAR(100) NOT NULL,
    		badge_icon VARCHAR(10) NOT NULL,
    		badge_color VARCHAR(20) NOT NULL,
    		created_at TIMESTAMP DEFAULT NOW(),
    		UNIQUE(user_id, badge_type)
		)`,

		`CREATE TABLE IF NOT EXISTS first_order_discount (
    		id SERIAL PRIMARY KEY,
    		discount_percent INTEGER DEFAULT 15,
    		is_active BOOLEAN DEFAULT true
		)`,

		`CREATE TABLE IF NOT EXISTS verification_requests (
    		id SERIAL PRIMARY KEY,
    		user_id INTEGER NOT NULL,
    		full_name VARCHAR(200) NOT NULL,
    		description TEXT NOT NULL,
    		contact VARCHAR(100) NOT NULL,
    		status VARCHAR(50) DEFAULT 'pending',
    		admin_comment TEXT,
    		created_at TIMESTAMP DEFAULT NOW(),
    		reviewed_at TIMESTAMP
		)`,

		`CREATE TABLE IF NOT EXISTS seller_profiles (
    		user_id INTEGER PRIMARY KEY,
    		banner_url VARCHAR(500) DEFAULT '',
    		avatar_url VARCHAR(500) DEFAULT '',
    		bio TEXT DEFAULT '',
    		custom_color VARCHAR(20) DEFAULT '#8b5cf6',
    		is_pro BOOLEAN DEFAULT false
		)`,

		`CREATE TABLE IF NOT EXISTS message_templates (
   			id SERIAL PRIMARY KEY,
   			user_id INTEGER NOT NULL,
   			title VARCHAR(200) NOT NULL,
   			content TEXT NOT NULL,
   			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS user_roles (
    		user_id INTEGER NOT NULL,
    		role VARCHAR(50) NOT NULL,
    		assigned_by INTEGER,
    		assigned_at TIMESTAMP DEFAULT NOW(),
    		PRIMARY KEY (user_id, role)
		)`,

		`CREATE TABLE IF NOT EXISTS admins (
            id SERIAL PRIMARY KEY,
            username VARCHAR(100) UNIQUE NOT NULL,
            password TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT NOW()
        )`,
	}

	for _, q := range queries {
		_, err := DB.Exec(q)
		if err != nil {
			log.Printf("❌ Ошибка создания таблицы: %v", err)
		}
	}

	log.Println("✅ Таблицы созданы")
}

func migrateColumns() {
	migrations := []string{
		`ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS device TEXT DEFAULT ''`,
		`ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS revoked BOOLEAN DEFAULT false`,
	}
	for _, q := range migrations {
		if _, err := DB.Exec(q); err != nil {
			log.Printf("⚠️ Миграция: %v", err)
		}
	}
}

func createIndexes() {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_users_referral ON users(referral_code)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_booster ON orders(booster_id)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_boost ON orders(boost_id)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_created ON orders(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_boosts_user ON boosts(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_boosts_game ON boosts(game)`,
		`CREATE INDEX IF NOT EXISTS idx_boosts_rating ON boosts(rating DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cart_user ON cart_items(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, is_read)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_order ON messages(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reviews_booster ON reviews(booster_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reviews_order ON reviews(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_transactions_user ON transaction_history(user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_referrals_referrer ON referrals(referrer_id)`,
		`CREATE INDEX IF NOT EXISTS idx_referrals_referred ON referrals(referred_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_token ON user_sessions(session_token)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON user_sessions(user_id, last_seen DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_push_user ON push_subscriptions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_qr_token ON qr_sessions(token)`,
		`CREATE INDEX IF NOT EXISTS idx_qr_user ON qr_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_qr_expires ON qr_sessions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_password_resets ON password_resets(user_id, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_disputes_order ON disputes(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_escrow_order ON escrow_transactions(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_user ON tickets(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_referral_earnings_user ON referral_earnings(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_banned ON banned_users(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_online ON user_online(last_seen)`,
		`CREATE INDEX IF NOT EXISTS idx_promocodes_code ON promocodes(code)`,
		`CREATE INDEX IF NOT EXISTS idx_sales_active ON sales(is_active, start_date, end_date)`,
	}

	for _, idx := range indexes {
		_, err := DB.Exec(idx)
		if err != nil {
			log.Printf("❌ Ошибка создания индекса: %v", err)
		}
	}

	log.Println("✅ Индексы созданы")
}

func startCleanupRoutine() {
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			DB.Exec("DELETE FROM qr_sessions WHERE expires_at < NOW()")
			DB.Exec("DELETE FROM user_online WHERE last_seen < NOW() - INTERVAL '1 hour'")
			DB.Exec("DELETE FROM notifications WHERE is_read = true AND created_at < NOW() - INTERVAL '30 days'")
			DB.Exec("DELETE FROM user_sessions WHERE last_seen < NOW() - INTERVAL '30 days'")
			log.Println("🧹 Очистка старых данных выполнена")
		}
	}()
}

func insertDefaultData() {
	var adminCount int
	DB.QueryRow("SELECT COUNT(*) FROM admins").Scan(&adminCount)
	if adminCount == 0 {
		randomPassword := randomString(16)
		hash, err := bcrypt.GenerateFromPassword([]byte(randomPassword), bcrypt.DefaultCost)
		if err != nil {
			log.Fatal("Failed to hash admin password:", err)
		}
		DB.Exec("INSERT INTO admins (username, password) VALUES ($1, $2)", "admin", string(hash))
		log.Printf("👑 Админ создан: admin / %s (сохраните этот пароль!)", randomPassword)
	}

	log.Println("✅ База данных готова. Товары добавляются пользователями.")

	var levelCount int
	DB.QueryRow("SELECT COUNT(*) FROM seller_levels").Scan(&levelCount)
	if levelCount == 0 {
		levels := []struct {
			level     int
			name      string
			minOrders int
			minRating float64
			icon      string
			color     string
		}{
			{1, "Новичок", 0, 0, "🌱", "#10b981"},
			{2, "Бронза", 5, 4.0, "🥉", "#cd7f32"},
			{3, "Серебро", 15, 4.3, "🥈", "#c0c0c0"},
			{4, "Золото", 30, 4.5, "🥇", "#ffd700"},
			{5, "Платина", 60, 4.7, "💎", "#e5e4e2"},
			{6, "Алмаз", 100, 4.8, "💠", "#b9f2ff"},
			{7, "Легенда", 200, 4.9, "👑", "#ff6b6b"},
		}

		for _, l := range levels {
			DB.Exec("INSERT INTO seller_levels (level, name, min_orders, min_rating, icon, color) VALUES ($1,$2,$3,$4,$5,$6)",
				l.level, l.name, l.minOrders, l.minRating, l.icon, l.color)
		}
	}

	var achCount int
	DB.QueryRow("SELECT COUNT(*) FROM achievements").Scan(&achCount)
	if achCount == 0 {
		achievements := []struct {
			name, desc, icon, field string
			value                   int
		}{
			{"Первый заказ", "Выполните первый заказ", "🎯", "orders", 1},
			{"5 заказов", "Выполните 5 заказов", "📦", "orders", 5},
			{"25 заказов", "Выполните 25 заказов", "🚀", "orders", 25},
			{"100 заказов", "Выполните 100 заказов", "💯", "orders", 100},
			{"Отличный рейтинг", "Достигните рейтинга 4.5", "⭐", "rating", 45},
			{"Мастер", "Достигните рейтинга 4.8", "🌟", "rating", 48},
			{"Заработал 10 000 ₽", "Заработайте 10 000 ₽", "💰", "earned", 10000},
			{"Заработал 100 000 ₽", "Заработайте 100 000 ₽", "💎", "earned", 100000},
		}

		for _, a := range achievements {
			DB.Exec("INSERT INTO achievements (name, description, icon, condition_field, condition_value) VALUES ($1,$2,$3,$4,$5)",
				a.name, a.desc, a.icon, a.field, a.value)
		}
	}

	var promoCount int
	DB.QueryRow("SELECT COUNT(*) FROM promocodes").Scan(&promoCount)
	if promoCount == 0 {
		promos := []struct {
			code     string
			discount int
			maxUses  int
		}{
			{"WELCOME10", 10, 100},
			{"SALE20", 20, 50},
			{"BOOST50", 50, 10},
			{"NEWYEAR", 30, 200},
		}

		for _, p := range promos {
			DB.Exec("INSERT INTO promocodes (code, discount_percent, max_uses, is_active) VALUES ($1,$2,$3,true)",
				p.code, p.discount, p.maxUses)
		}
	}

	var firstDiscountCount int
	DB.QueryRow("SELECT COUNT(*) FROM first_order_discount").Scan(&firstDiscountCount)
	if firstDiscountCount == 0 {
		DB.Exec("INSERT INTO first_order_discount (discount_percent, is_active) VALUES (15, true)")
	}

	var salesCount int
	DB.QueryRow("SELECT COUNT(*) FROM sales").Scan(&salesCount)
	if salesCount == 0 {
		sales := []struct {
			name     string
			discount int
			start    string
			end      string
		}{
			{"Летняя распродажа", 25, "2025-06-01", "2025-08-31"},
			{"Новогодняя акция", 20, "2025-12-20", "2026-01-10"},
			{"Черная пятница", 40, "2025-11-25", "2025-11-30"},
		}

		for _, s := range sales {
			DB.Exec("INSERT INTO sales (name, discount_percent, start_date, end_date) VALUES ($1,$2,$3,$4)",
				s.name, s.discount, s.start, s.end)
		}
	}

	DB.Exec("INSERT INTO user_badges (user_id, badge_type, badge_name, badge_icon, badge_color) VALUES (1, 'developer', 'Разработчик', '⚙️', '#8b5cf6') ON CONFLICT DO NOTHING")
}
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
