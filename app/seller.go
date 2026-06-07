package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"nexus-boost/database"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func boosterPage(c *gin.Context) {
	user, _ := c.Get("user")
	id := c.Param("id")

	var b Boost
	err := database.DB.QueryRow(
		"SELECT b.id, b.game, b.title, b.description, b.price, b.rating, b.reviews, b.user_id, u.username FROM boosts b LEFT JOIN users u ON b.user_id = u.id WHERE b.id = $1",
		id,
	).Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price, &b.Rating, &b.Reviews, &b.BoosterID, &b.OwnerName)

	if err != nil {
		c.String(404, "Товар не найден")
		return
	}

	// Отзывы на этот товар
	revRows, _ := database.DB.Query(
		`SELECT r.id, r.rating, r.text, r.username, r.created_at 
         FROM reviews r 
         JOIN orders o ON r.order_id = o.id 
         WHERE o.boost_id = $1 
         ORDER BY r.created_at DESC LIMIT 20`,
		id,
	)
	if revRows != nil {
		defer revRows.Close()
	}

	type Review struct {
		ID        int
		Rating    int
		Text      string
		Username  string
		CreatedAt time.Time
	}

	var reviews []Review
	if revRows != nil {
		for revRows.Next() {
			var r Review
			revRows.Scan(&r.ID, &r.Rating, &r.Text, &r.Username, &r.CreatedAt)
			reviews = append(reviews, r)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "",
		"Data":   gin.H{"Boost": b, "Reviews": reviews},
	})
}

func sellerPage(c *gin.Context) {
	user, _ := c.Get("user")
	id := c.Param("id")

	// ОДИН запрос для продавца + его статистики
	var s User
	var isOnline bool
	var lastSeen time.Time
	var salesCount int
	var isPro bool
	var bannerURL, avatarURL, bio, customColor string

	err := database.DB.QueryRow(`
        SELECT 
            u.id, u.username, u.rating, u.reviews, u.orders, u.created_at,
            EXISTS(SELECT 1 FROM user_online WHERE user_id = u.id AND last_seen > NOW() - INTERVAL '1 minute'),
            COALESCE((SELECT last_seen FROM user_online WHERE user_id = u.id), NOW()),
            COALESCE((SELECT COUNT(*) FROM orders WHERE booster_id = u.id AND status = 'completed'), 0),
            COALESCE(sp.banner_url, ''), COALESCE(sp.avatar_url, ''), 
            COALESCE(sp.bio, ''), COALESCE(sp.custom_color, '#8b5cf6'),
            COALESCE(sp.is_pro, false)
        FROM users u
        LEFT JOIN seller_profiles sp ON u.id = sp.user_id
        WHERE u.id = $1
    `, id).Scan(
		&s.ID, &s.Username, &s.Rating, &s.Reviews, &s.Orders, &s.CreatedAt,
		&isOnline, &lastSeen, &salesCount,
		&bannerURL, &avatarURL, &bio, &customColor, &isPro,
	)

	if err != nil {
		c.String(404, "Продавец не найден")
		return
	}

	// Остальное (товары, отзывы, бейджи) — отдельно, но с LIMIT
	badges := getUserBadges(s.ID)

	bRows, _ := database.DB.Query("SELECT id, game, title, description, price, rating, reviews FROM boosts WHERE user_id = $1 LIMIT 20", id)
	defer bRows.Close()
	var boosts []Boost
	for bRows.Next() {
		var b Boost
		bRows.Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price, &b.Rating, &b.Reviews)
		boosts = append(boosts, b)
	}

	revRows, _ := database.DB.Query("SELECT id, rating, text, username, created_at FROM reviews WHERE booster_id = $1 ORDER BY created_at DESC LIMIT 20", id)
	defer revRows.Close()
	type Review struct {
		ID        int
		Rating    int
		Text      string
		Username  string
		CreatedAt time.Time
	}
	var reviews []Review
	for revRows.Next() {
		var r Review
		revRows.Scan(&r.ID, &r.Rating, &r.Text, &r.Username, &r.CreatedAt)
		reviews = append(reviews, r)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":     user,
		"Active":   "seller",
		"IsPro":    isPro,
		"IsOnline": isOnline,
		"LastSeen": lastSeen,
		"Data": gin.H{
			"Seller":      s,
			"Boosts":      boosts,
			"Reviews":     reviews,
			"Badges":      badges,
			"SalesCount":  salesCount,
			"BannerURL":   bannerURL,
			"AvatarURL":   avatarURL,
			"Bio":         bio,
			"CustomColor": customColor,
			"IsPro":       isPro,
		},
	})
}

func profilePage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Обновляем онлайн
	database.DB.Exec("INSERT INTO user_online (user_id, last_seen) VALUES ($1, NOW()) ON CONFLICT (user_id) DO UPDATE SET last_seen = NOW()", userID)

	// ОДИН запрос вместо 5
	var user User
	var isPro bool
	var proExpires time.Time
	var isVerified, hasPending bool
	var lastSeen time.Time
	var totalSales, completedOrders, activeOrders int
	var totalEarned, avgRating float64

	err := database.DB.QueryRow(`
        SELECT 
            u.id, u.username, u.email, u.balance, u.level, u.orders,
            COALESCE(sp.is_pro, false),
            COALESCE(sp.pro_expires, NOW()),
            EXISTS(SELECT 1 FROM user_badges WHERE user_id = u.id AND badge_type = 'verified'),
            EXISTS(SELECT 1 FROM verification_requests WHERE user_id = u.id AND status = 'pending'),
            COALESCE(uo.last_seen, NOW() - INTERVAL '10 minutes'),
            COALESCE(ord_stats.total_sales, 0),
            COALESCE(ord_stats.total_earned, 0),
            COALESCE(ord_stats.completed, 0),
            COALESCE(ord_stats.active, 0),
            COALESCE(rev_stats.avg_rating, 0)
        FROM users u
        LEFT JOIN seller_profiles sp ON u.id = sp.user_id
        LEFT JOIN user_online uo ON u.id = uo.user_id
        LEFT JOIN LATERAL (
            SELECT 
                COUNT(*) as total_sales,
                COALESCE(SUM(total) FILTER (WHERE status = 'completed'), 0) as total_earned,
                COUNT(*) FILTER (WHERE status = 'completed') as completed,
                COUNT(*) FILTER (WHERE status IN ('processing', 'ready')) as active
            FROM orders WHERE booster_id = u.id
        ) ord_stats ON true
        LEFT JOIN LATERAL (
            SELECT AVG(rating) as avg_rating FROM reviews WHERE booster_id = u.id
        ) rev_stats ON true
        WHERE u.id = $1
    `, userID).Scan(
		&user.ID, &user.Username, &user.Email, &user.Balance, &user.Level, &user.Orders,
		&isPro, &proExpires, &isVerified, &hasPending, &lastSeen,
		&totalSales, &totalEarned, &completedOrders, &activeOrders, &avgRating,
	)

	if err != nil {
		c.Redirect(302, "/login")
		return
	}

	csrfToken := randomString(32)
	session.Values["csrf_token"] = csrfToken
	session.Save(c.Request, c.Writer)

	isOnline := time.Since(lastSeen) < 5*time.Minute

	// Заказы (покупки) — одним запросом
	rows, _ := database.DB.Query("SELECT id, title, game, booster, total, status, created_at FROM orders WHERE user_id = $1 ORDER BY created_at DESC LIMIT 10", user.ID)
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		rows.Scan(&o.ID, &o.Title, &o.Game, &o.Booster, &o.Total, &o.Status, &o.CreatedAt)
		orders = append(orders, o)
	}

	// Мои товары
	bRows, _ := database.DB.Query("SELECT id, game, title, description, price FROM boosts WHERE user_id = $1 LIMIT 10", user.ID)
	defer bRows.Close()
	var myBoosts []Boost
	for bRows.Next() {
		var b Boost
		bRows.Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price)
		myBoosts = append(myBoosts, b)
	}

	// Транзакции — LIMIT в запросе
	txRows, _ := database.DB.Query("SELECT type, amount, description, created_at FROM transaction_history WHERE user_id = $1 ORDER BY created_at DESC LIMIT 10", user.ID)
	defer txRows.Close()
	type TxItem struct {
		Type        string
		Amount      float64
		Description string
		CreatedAt   time.Time
	}
	var transactions []TxItem
	for txRows.Next() {
		var t TxItem
		txRows.Scan(&t.Type, &t.Amount, &t.Description, &t.CreatedAt)
		transactions = append(transactions, t)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":       &user,
		"Active":     "profile",
		"csrf":       csrfToken,
		"Success":    c.Query("success"),
		"Error":      c.Query("error"),
		"IsPro":      isPro,
		"IsOnline":   isOnline,
		"LastSeen":   lastSeen,
		"IsVerified": isVerified,
		"HasPending": hasPending,
		"ProExpires": proExpires,
		"Data": gin.H{
			"Orders":          orders,
			"MyBoosts":        myBoosts,
			"Transactions":    transactions,
			"TotalSales":      totalSales,
			"TotalEarned":     totalEarned,
			"CompletedOrders": completedOrders,
			"ActiveOrders":    activeOrders,
			"AvgRating":       avgRating,
		},
	})
}

func editProfilePage(c *gin.Context) { render(c, "edit-profile", nil) }

func addBoostPage(c *gin.Context) { render(c, "add-boost", nil) }

func updateProfile(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	database.DB.Exec("UPDATE users SET username=$1, email=$2 WHERE id=$3", c.PostForm("username"), c.PostForm("email"), user.ID)
	setUser(c, user.ID, c.PostForm("username"))
	c.Redirect(302, "/profile")
}

func addBoost(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	if user == nil {
		c.Redirect(302, "/login")
		return
	}

	game := c.PostForm("game")
	title := c.PostForm("title")
	desc := c.PostForm("description")
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)

	if game == "" || title == "" || price <= 0 {
		render(c, "add-boost", gin.H{"Error": "Все поля обязательны для заполнения"})
		return
	}

	_, err := database.DB.Exec(
		"INSERT INTO boosts (game, title, description, price, user_id) VALUES ($1,$2,$3,$4,$5)",
		game, title, desc, price, user.ID,
	)

	if err != nil {
		render(c, "add-boost", gin.H{"Error": "Ошибка добавления предложения"})
		return
	}

	c.Redirect(302, "/profile?success=Предложение+добавлено")
}

func sellerOrdersPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]
	var user User
	database.DB.QueryRow("SELECT id, username, email, balance FROM users WHERE id=$1", userID).Scan(&user.ID, &user.Username, &user.Email, &user.Balance)
	rows, _ := database.DB.Query("SELECT id, user_id, boost_id, title, game, booster, total, status, created_at FROM orders WHERE booster_id=$1 ORDER BY created_at DESC", userID)
	if rows != nil {
		defer rows.Close()
	}
	var orders []Order
	if rows != nil {
		for rows.Next() {
			var o Order
			rows.Scan(&o.ID, &o.UserID, &o.BoostID, &o.Title, &o.Game, &o.Booster, &o.Total, &o.Status, &o.CreatedAt)
			orders = append(orders, o)
		}
	}
	layoutHTML(c, http.StatusOK, gin.H{"User": &user, "Active": "seller-orders", "Data": gin.H{"Orders": orders}})
}

func sellerOrderDetailPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro)
	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	orderID := c.Param("id")

	var o Order
	var buyerName string
	err := database.DB.QueryRow(
		`SELECT o.id, o.user_id, o.boost_id, o.booster_id, o.title, o.game, o.total, o.status, o.created_at, u.username 
         FROM orders o 
         LEFT JOIN users u ON o.user_id = u.id 
         WHERE o.id = $1 AND o.booster_id = $2`,
		orderID, userID,
	).Scan(&o.ID, &o.UserID, &o.BoostID, &o.BoosterID, &o.Title, &o.Game, &o.Total, &o.Status, &o.CreatedAt, &buyerName)

	if err != nil {
		c.String(404, "Заказ не найден")
		return
	}

	o.Booster = buyerName

	// Сообщения
	rows, _ := database.DB.Query("SELECT id, user_id, username, text, created_at FROM messages WHERE order_id = $1 ORDER BY created_at ASC", o.ID)
	if rows != nil {
		defer rows.Close()
	}
	var messages []Message
	if rows != nil {
		for rows.Next() {
			var m Message
			rows.Scan(&m.ID, &m.UserID, &m.Username, &m.Text, &m.CreatedAt)
			messages = append(messages, m)
		}
	}
	if messages == nil {
		messages = []Message{}
	}

	// Проверяем есть ли спор
	var disputeID int
	database.DB.QueryRow("SELECT COALESCE((SELECT id FROM disputes WHERE order_id = $1), 0)", orderID).Scan(&disputeID)

	csrfToken := randomString(32)
	session.Values["csrf_token"] = csrfToken
	session.Save(c.Request, c.Writer)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "seller-order",
		"IsPro":  isPro,
		"csrf":   csrfToken,
		"Data": gin.H{
			"Order":     o,
			"Messages":  messages,
			"DisputeID": disputeID,
			"IsPro":     isPro,
		},
	})
}

func sellerSendMessage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	orderID := c.Param("id")
	var boosterID int
	database.DB.QueryRow("SELECT booster_id FROM orders WHERE id = $1", orderID).Scan(&boosterID)
	if boosterID != userID {
		c.Redirect(302, "/seller/orders?error=Доступ+запрещён")
		return
	}

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	text := strings.TrimSpace(c.PostForm("message"))

	if text != "" {
		database.DB.Exec("INSERT INTO messages (order_id, user_id, username, text) VALUES ($1,$2,$3,$4)",
			orderID, userID, username, text)

		// Получаем user_id покупателя
		var buyerID int
		err := database.DB.QueryRow("SELECT user_id FROM orders WHERE id = $1", orderID).Scan(&buyerID)
		if err == nil && buyerID > 0 {
			// Уведомление покупателю
			notifText := fmt.Sprintf("💬 Ответ от продавца %s в заказе #%s", username, orderID)
			database.DB.Exec(
				"INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
				buyerID, notifText, "/order/"+orderID,
			)
			go sendPushNotification(buyerID, "Новое сообщение", notifText, "/order/"+orderID)
		}
	}

	c.Redirect(302, "/seller/order/"+orderID)
}

func editBoostPage(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	if user == nil {
		c.Redirect(302, "/login")
		return
	}

	id := c.Param("id")
	var b Boost
	err := database.DB.QueryRow(
		"SELECT id, game, title, description, price FROM boosts WHERE id = $1 AND user_id = $2",
		id, user.ID,
	).Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price)

	if err != nil {
		c.Redirect(302, "/profile?error=Товар+не+найден")
		return
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "edit-boost",
		"Data":   gin.H{"Boost": b},
	})
}

func editBoost(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	if user == nil {
		c.Redirect(302, "/login")
		return
	}

	id := c.Param("id")
	game := c.PostForm("game")
	title := c.PostForm("title")
	desc := c.PostForm("description")
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)

	database.DB.Exec(
		"UPDATE boosts SET game = $1, title = $2, description = $3, price = $4 WHERE id = $5 AND user_id = $6",
		game, title, desc, price, id, user.ID,
	)

	c.Redirect(302, "/profile?success=Товар+обновлен")
}

// Отзывы

func sellerMarkReady(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("id")

	// Проверяем что это продавец этого заказа
	var boosterID int
	database.DB.QueryRow("SELECT booster_id FROM orders WHERE id = $1", orderID).Scan(&boosterID)

	if boosterID != userID {
		c.Redirect(302, "/seller/orders?error=Нет+доступа")
		return
	}

	// Обновляем статус на "ready" (готово, ожидает подтверждения)
	database.DB.Exec("UPDATE orders SET status = 'ready' WHERE id = $1", orderID)

	// Сообщение в чат
	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)
	database.DB.Exec("INSERT INTO messages (order_id, user_id, username, text) VALUES ($1,$2,$3,'✅ Продавец отметил заказ как выполненный. Ожидается подтверждение покупателя.')",
		orderID, userID, username)

	// Уведомление покупателю
	var buyerID int
	database.DB.QueryRow("SELECT user_id FROM orders WHERE id = $1", orderID).Scan(&buyerID)
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		buyerID,
		fmt.Sprintf("📦 Продавец выполнил заказ #%s. Подтвердите выполнение!", orderID),
		"/order/"+orderID)

	notifText := fmt.Sprintf("📦 Продавец выполнил заказ #%s. Подтвердите выполнение!", orderID)
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		buyerID, notifText, "/order/"+orderID)

	go sendPushNotification(buyerID, "Заказ выполнен", notifText, "/order/"+orderID)

	c.Redirect(302, "/seller/order/"+orderID)
}

// Выдать бейдж

func editSellerProfilePage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	// Загружаем текущий профиль
	var bannerURL, avatarURL, bio, customColor string
	var isPro bool
	database.DB.QueryRow(
		"SELECT COALESCE(banner_url, ''), COALESCE(avatar_url, ''), COALESCE(bio, ''), COALESCE(custom_color, '#8b5cf6'), COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1",
		userID,
	).Scan(&bannerURL, &avatarURL, &bio, &customColor, &isPro)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "edit-seller-profile",
		"Data": gin.H{
			"BannerURL":   bannerURL,
			"AvatarURL":   avatarURL,
			"Bio":         bio,
			"CustomColor": customColor,
			"IsPro":       isPro,
		},
	})
}

// Сохранение профиля

func saveSellerProfile(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	bannerURL := c.PostForm("banner_url")
	avatarURL := c.PostForm("avatar_url")
	bio := c.PostForm("bio")
	customColor := c.PostForm("custom_color")

	database.DB.Exec(`
        INSERT INTO seller_profiles (user_id, banner_url, avatar_url, bio, custom_color) 
        VALUES ($1,$2,$3,$4,$5) 
        ON CONFLICT (user_id) DO UPDATE SET banner_url=$2, avatar_url=$3, bio=$4, custom_color=$5
    `, userID, bannerURL, avatarURL, bio, customColor)

	c.Redirect(302, "/seller/"+fmt.Sprintf("%d", userID))
}

func upgradeProPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username, balance FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username, &user.Balance)

	// Проверяем есть ли уже PRO
	var isPro bool
	var proExpires time.Time
	database.DB.QueryRow("SELECT COALESCE(is_pro, false), COALESCE(pro_expires, NOW()) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro, &proExpires)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":       &user,
		"Active":     "upgrade-pro",
		"IsPro":      isPro,
		"ProExpires": proExpires,
	})
}

func buyPro(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	duration := c.PostForm("duration")

	var price float64
	var months int

	switch duration {
	case "trial":
		price = 0
		months = 0 // пробный период 7 дней
	case "month":
		price = 100
		months = 1
	case "year":
		price = 500
		months = 12
	default:
		c.Redirect(302, "/upgrade-pro?error=Неверный+период")
		return
	}

	// Для пробного периода не списываем деньги
	if price > 0 {
		var balance float64
		database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", userID).Scan(&balance)

		if balance < price {
			c.Redirect(302, "/upgrade-pro?error=Недостаточно+средств")
			return
		}

		// Списываем деньги
		database.DB.Exec("UPDATE users SET balance = balance - $1 WHERE id = $2", price, userID)

		// История транзакций
		var newBalance float64
		database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", userID).Scan(&newBalance)
		database.DB.Exec(
			"INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description) VALUES ($1,'payment',$2,$3,$4,'PRO подписка')",
			userID, price, newBalance+price, newBalance,
		)
	}

	// Проверяем есть ли уже запись в seller_profiles
	var exists bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM seller_profiles WHERE user_id = $1)", userID).Scan(&exists)

	if exists {
		// Обновляем существующую запись
		if months == 0 {
			// Пробный период 7 дней
			database.DB.Exec("UPDATE seller_profiles SET is_pro = true, pro_expires = NOW() + INTERVAL '7 days' WHERE user_id = $1", userID)
		} else {
			database.DB.Exec(
				"UPDATE seller_profiles SET is_pro = true, pro_expires = COALESCE(pro_expires, NOW()) + INTERVAL '1 month' * $1 WHERE user_id = $2",
				months, userID,
			)
		}
	} else {
		// Создаем новую запись
		if months == 0 {
			database.DB.Exec(
				"INSERT INTO seller_profiles (user_id, is_pro, pro_expires) VALUES ($1, true, NOW() + INTERVAL '7 days')",
				userID,
			)
		} else {
			database.DB.Exec(
				"INSERT INTO seller_profiles (user_id, is_pro, pro_expires) VALUES ($1, true, NOW() + INTERVAL '1 month' * $2)",
				userID, months,
			)
		}
	}

	if price == 0 {
		c.Redirect(302, "/profile?success=Пробный+PRO+активирован+на+7+дней!")
	} else {
		c.Redirect(302, "/profile?success=PRO+активирован+на+"+fmt.Sprintf("%d", months)+"+мес!")
	}
}

func sellerStatsPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	// Проверяем PRO
	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro)

	if !isPro {
		c.Redirect(302, "/upgrade-pro")
		return
	}

	// Общая статистика
	var totalSales, completedOrders int
	var totalEarned, avgOrderValue float64
	database.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(total), 0) FROM orders WHERE booster_id = $1", userID).Scan(&totalSales, &totalEarned)
	database.DB.QueryRow("SELECT COUNT(*), COALESCE(AVG(total), 0) FROM orders WHERE booster_id = $1 AND status = 'completed'", userID).Scan(&completedOrders, &avgOrderValue)

	// Продажи по дням за 30 дней
	rows, _ := database.DB.Query(`
        SELECT DATE(created_at) as day, COUNT(*), COALESCE(SUM(total), 0)
        FROM orders 
        WHERE booster_id = $1 AND created_at > NOW() - INTERVAL '30 days'
        GROUP BY DATE(created_at)
        ORDER BY day DESC
        LIMIT 30
    `, userID)
	defer rows.Close()

	type DayStat struct {
		Date  string
		Count int
		Total float64
	}

	var dailyStats []DayStat
	for rows.Next() {
		var d DayStat
		var day time.Time
		rows.Scan(&day, &d.Count, &d.Total)
		d.Date = day.Format("02.01")
		dailyStats = append(dailyStats, d)
	}

	// Популярные игры
	gameRows, _ := database.DB.Query(`
        SELECT game, COUNT(*) as cnt, COALESCE(SUM(total), 0) as total
        FROM orders 
        WHERE booster_id = $1
        GROUP BY game
        ORDER BY cnt DESC
    `, userID)
	defer gameRows.Close()

	type GameStat struct {
		Name  string
		Count int
		Total float64
	}

	var gameStats []GameStat
	for gameRows.Next() {
		var g GameStat
		gameRows.Scan(&g.Name, &g.Count, &g.Total)
		gameStats = append(gameStats, g)
	}

	// Рейтинг по месяцам
	ratingRows, _ := database.DB.Query(`
        SELECT TO_CHAR(created_at, 'YYYY-MM'), AVG(rating)::numeric(3,2), COUNT(*)
        FROM reviews 
        WHERE booster_id = $1
        GROUP BY TO_CHAR(created_at, 'YYYY-MM')
        ORDER BY 1 DESC
        LIMIT 12
    `, userID)
	defer ratingRows.Close()

	type RatingStat struct {
		Month  string
		Rating float64
		Count  int
	}

	var ratingStats []RatingStat
	for ratingRows.Next() {
		var r RatingStat
		ratingRows.Scan(&r.Month, &r.Rating, &r.Count)
		ratingStats = append(ratingStats, r)
	}

	// Доход по месяцам за год
	monthlyRows, _ := database.DB.Query(`
    SELECT TO_CHAR(created_at, 'YYYY-MM') as month, 
           COUNT(*) as orders,
           COALESCE(SUM(total), 0) as revenue,
           ROUND(AVG(total)) as avg_order
    FROM orders 
    WHERE booster_id = $1 AND status = 'completed'
    GROUP BY month
    ORDER BY month DESC LIMIT 12
`, userID)
	defer monthlyRows.Close()

	type MonthlyStat struct {
		Month    string
		Orders   int
		Revenue  float64
		AvgOrder float64
	}
	var monthlyStats []MonthlyStat
	for monthlyRows.Next() {
		var m MonthlyStat
		monthlyRows.Scan(&m.Month, &m.Orders, &m.Revenue, &m.AvgOrder)
		monthlyStats = append(monthlyStats, m)
	}

	// Конверсия просмотров в продажи
	var totalViews, totalOrders int
	database.DB.QueryRow("SELECT COUNT(*) FROM boost_views WHERE boost_id IN (SELECT id FROM boosts WHERE user_id = $1)", userID).Scan(&totalViews)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE booster_id = $1", userID).Scan(&totalOrders)
	conversion := 0.0
	if totalViews > 0 {
		conversion = float64(totalOrders) / float64(totalViews) * 100
	}

	// Топ товаров
	topBoostRows, _ := database.DB.Query(`
    SELECT b.title, COUNT(o.id) as sales, COALESCE(SUM(o.total), 0) as revenue
    FROM boosts b
    LEFT JOIN orders o ON b.id = o.boost_id AND o.status = 'completed'
    WHERE b.user_id = $1
    GROUP BY b.id, b.title
    ORDER BY sales DESC LIMIT 5
`, userID)
	defer topBoostRows.Close()

	type TopBoost struct {
		Title   string
		Sales   int
		Revenue float64
	}
	var topBoosts []TopBoost
	for topBoostRows.Next() {
		var tb TopBoost
		topBoostRows.Scan(&tb.Title, &tb.Sales, &tb.Revenue)
		topBoosts = append(topBoosts, tb)
	}

	// Время выполнения заказов
	var avgCompletionHours float64
	database.DB.QueryRow(`
    SELECT ROUND(AVG(EXTRACT(EPOCH FROM (created_at - NOW() + INTERVAL '1 day'))/3600))
    FROM orders WHERE booster_id = $1 AND status = 'completed'
`, userID).Scan(&avgCompletionHours)

	// Лучший день недели
	weekdayRows, _ := database.DB.Query(`
    SELECT TRIM(TO_CHAR(created_at, 'Day')) as day, COUNT(*) as cnt
    FROM orders WHERE booster_id = $1 AND status = 'completed'
    GROUP BY day ORDER BY cnt DESC LIMIT 1
`, userID)
	defer weekdayRows.Close()
	var bestDay string
	var bestDayCount int
	if weekdayRows.Next() {
		weekdayRows.Scan(&bestDay, &bestDayCount)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "seller-stats",
		"Data": gin.H{
			"TotalSales":         totalSales,
			"CompletedOrders":    completedOrders,
			"TotalEarned":        totalEarned,
			"AvgOrderValue":      avgOrderValue,
			"DailyStats":         dailyStats,
			"GameStats":          gameStats,
			"RatingStats":        ratingStats,
			"MonthlyStats":       monthlyStats,
			"TotalViews":         totalViews,
			"Conversion":         conversion,
			"TopBoosts":          topBoosts,
			"AvgCompletionHours": avgCompletionHours,
			"BestDay":            bestDay,
			"BestDayCount":       bestDayCount,
		},
	})
}

func boostItem(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	// Проверяем PRO
	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro)
	if !isPro {
		c.Redirect(302, "/upgrade-pro")
		return
	}

	boostID := c.Param("id")
	// Обновляем дату создания товара (поднимает в поиске)
	database.DB.Exec("UPDATE boosts SET created_at = NOW() WHERE id = $1 AND user_id = $2", boostID, userID)

	c.Redirect(302, "/profile?success=Товар+поднят+в+топ!")
}

func bulkCreateBoosts(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro)
	if !isPro {
		c.Redirect(302, "/upgrade-pro")
		return
	}

	// Парсим JSON с товарами
	var boosts []struct {
		Game  string  `json:"game"`
		Title string  `json:"title"`
		Desc  string  `json:"desc"`
		Price float64 `json:"price"`
	}
	json.NewDecoder(c.Request.Body).Decode(&boosts)

	for _, b := range boosts {
		database.DB.Exec("INSERT INTO boosts (game, title, description, price, user_id) VALUES ($1,$2,$3,$4,$5)",
			b.Game, b.Title, b.Desc, b.Price, userID)
	}

	c.JSON(200, gin.H{"success": true, "count": len(boosts)})
}

func sellerInsights(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro)
	if !isPro {
		c.Redirect(302, "/upgrade-pro")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	// 1. Общая аналитика рынка
	rows, _ := database.DB.Query(`
        SELECT game, COUNT(*), ROUND(AVG(price)), MIN(price), MAX(price), COUNT(DISTINCT user_id)
        FROM boosts GROUP BY game ORDER BY COUNT(*) DESC
    `)
	type Insight struct {
		Game     string
		Offers   int
		AvgPrice float64
		MinPrice float64
		MaxPrice float64
		Sellers  int
	}
	var insights []Insight
	if rows != nil {
		for rows.Next() {
			var in Insight
			rows.Scan(&in.Game, &in.Offers, &in.AvgPrice, &in.MinPrice, &in.MaxPrice, &in.Sellers)
			insights = append(insights, in)
		}
		rows.Close()
	}

	// 2. Мои позиции
	myRows, _ := database.DB.Query("SELECT game, COUNT(*), ROUND(AVG(price)), MIN(price) FROM boosts WHERE user_id = $1 GROUP BY game", userID)
	myPositions := make(map[string]map[string]interface{})
	if myRows != nil {
		for myRows.Next() {
			var game string
			var offers int
			var avg, min float64
			myRows.Scan(&game, &offers, &avg, &min)
			myPositions[game] = map[string]interface{}{"offers": offers, "avg": avg, "min": min}
		}
		myRows.Close()
	}

	// 3. Топ продавцов по количеству товаров
	topSellers, _ := database.DB.Query(`
        SELECT u.username, COUNT(b.id) as offers, ROUND(AVG(b.price)) as avg_price, 
               COALESCE(sp.is_pro, false) as is_pro
        FROM boosts b JOIN users u ON b.user_id = u.id 
        LEFT JOIN seller_profiles sp ON u.id = sp.user_id
        GROUP BY u.username, sp.is_pro
        ORDER BY offers DESC LIMIT 10
    `)
	type TopSeller struct {
		Username string
		Offers   int
		AvgPrice float64
		IsPro    bool
	}
	var topSellersList []TopSeller
	if topSellers != nil {
		for topSellers.Next() {
			var ts TopSeller
			topSellers.Scan(&ts.Username, &ts.Offers, &ts.AvgPrice, &ts.IsPro)
			topSellersList = append(topSellersList, ts)
		}
		topSellers.Close()
	}

	// 4. Самые дорогие и дешевые игры
	expensiveRows, _ := database.DB.Query(`SELECT game, ROUND(AVG(price)) as avg FROM boosts GROUP BY game ORDER BY avg DESC LIMIT 5`)
	type PriceExtreme struct {
		Game string
		Avg  float64
	}
	var expensiveGames []PriceExtreme
	if expensiveRows != nil {
		for expensiveRows.Next() {
			var pe PriceExtreme
			expensiveRows.Scan(&pe.Game, &pe.Avg)
			expensiveGames = append(expensiveGames, pe)
		}
		expensiveRows.Close()
	}

	cheapRows, _ := database.DB.Query(`SELECT game, ROUND(AVG(price)) as avg FROM boosts GROUP BY game ORDER BY avg ASC LIMIT 5`)
	var cheapGames []PriceExtreme
	if cheapRows != nil {
		for cheapRows.Next() {
			var pe PriceExtreme
			cheapRows.Scan(&pe.Game, &pe.Avg)
			cheapGames = append(cheapGames, pe)
		}
		cheapRows.Close()
	}

	// 5. Рекомендации для моих товаров
	myBoosts, _ := database.DB.Query("SELECT id, game, title, price FROM boosts WHERE user_id = $1", userID)
	type MyBoostRec struct {
		ID             int
		Game           string
		Title          string
		MyPrice        float64
		MarketAvg      float64
		MarketMin      float64
		MarketMax      float64
		Recommendation string
	}
	var myBoostsRecs []MyBoostRec
	if myBoosts != nil {
		for myBoosts.Next() {
			var mbr MyBoostRec
			myBoosts.Scan(&mbr.ID, &mbr.Game, &mbr.Title, &mbr.MyPrice)
			// Ищем среднюю цену по рынку для этой игры
			for _, in := range insights {
				if in.Game == mbr.Game {
					mbr.MarketAvg = in.AvgPrice
					mbr.MarketMin = in.MinPrice
					mbr.MarketMax = in.MaxPrice
					if mbr.MyPrice > in.AvgPrice*1.2 {
						mbr.Recommendation = "📉 Цена выше рынка на 20%+. Рекомендуем снизить."
					} else if mbr.MyPrice < in.AvgPrice*0.8 {
						mbr.Recommendation = "📈 Цена ниже рынка. Можно поднять."
					} else {
						mbr.Recommendation = "✅ Цена в рынке."
					}
					break
				}
			}
			myBoostsRecs = append(myBoostsRecs, mbr)
		}
		myBoosts.Close()
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "seller-insights",
		"Data": gin.H{
			"Insights":       insights,
			"MyPositions":    myPositions,
			"TopSellers":     topSellersList,
			"ExpensiveGames": expensiveGames,
			"CheapGames":     cheapGames,
			"MyBoostsRecs":   myBoostsRecs,
		},
	})
}

// Детали по игре

func sellerInsightsGameDetail(c *gin.Context) {
	game := c.Param("game")

	// Все предложения по игре
	rows, _ := database.DB.Query(`
        SELECT b.price, b.title, u.username, b.rating, b.reviews, b.created_at,
               COALESCE(sp.is_pro, false) as is_pro
        FROM boosts b
        JOIN users u ON b.user_id = u.id
        LEFT JOIN seller_profiles sp ON u.id = sp.user_id
        WHERE b.game = $1
        ORDER BY b.price ASC
    `, game)
	defer rows.Close()

	type Offer struct {
		Price     float64
		Title     string
		Username  string
		Rating    float64
		Reviews   int
		CreatedAt time.Time
		IsPro     bool
	}
	var offers []Offer
	for rows.Next() {
		var o Offer
		rows.Scan(&o.Price, &o.Title, &o.Username, &o.Rating, &o.Reviews, &o.CreatedAt, &o.IsPro)
		offers = append(offers, o)
	}

	c.JSON(200, offers)
}

func sellerTemplates(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Проверяем PRO
	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro)

	if !isPro {
		c.Redirect(302, "/upgrade-pro")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	rows, err := database.DB.Query("SELECT id, title, content FROM message_templates WHERE user_id = $1", userID)
	if err != nil {
		log.Printf("Ошибка запроса шаблонов: %v", err)
	}
	if rows != nil {
		defer rows.Close()
	}

	type Template struct {
		ID      int
		Title   string
		Content string
	}

	var templates []Template
	if rows != nil {
		for rows.Next() {
			var t Template
			rows.Scan(&t.ID, &t.Title, &t.Content)
			templates = append(templates, t)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "seller-templates",
		"Data":   gin.H{"Templates": templates},
	})
}

func saveTemplate(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	title := c.PostForm("title")
	content := c.PostForm("content")

	if title != "" && content != "" {
		_, err := database.DB.Exec(
			"INSERT INTO message_templates (user_id, title, content) VALUES ($1, $2, $3)",
			userID, title, content,
		)
		if err != nil {
			log.Printf("Ошибка сохранения шаблона: %v", err)
		}
	}

	c.Redirect(302, "/seller/templates")
}

func deleteTemplate(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	templateID := c.Param("id")
	database.DB.Exec("DELETE FROM message_templates WHERE id = $1 AND user_id = $2", templateID, userID)

	c.Redirect(302, "/seller/templates")
}

func editTemplate(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	templateID := c.Param("id")
	title := c.PostForm("title")
	content := c.PostForm("content")

	database.DB.Exec("UPDATE message_templates SET title = $1, content = $2 WHERE id = $3 AND user_id = $4",
		title, content, templateID, userID)

	c.Redirect(302, "/seller/templates")
}

func processReferralEarnings(userID int, orderID int, amount float64) {
	// Ищем кто пригласил этого пользователя (уровень 1)
	var referrerID int
	database.DB.QueryRow(
		"SELECT referrer_id FROM referrals WHERE referred_id = $1 AND level = 1",
		userID,
	).Scan(&referrerID)

	if referrerID > 0 {
		// Начисляем 5% прямому рефереру
		earn1 := amount * 0.05
		database.DB.Exec("UPDATE referrals SET earnings = earnings + $1 WHERE referrer_id = $2 AND referred_id = $3",
			earn1, referrerID, userID)
		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", earn1, referrerID)
		database.DB.Exec(`INSERT INTO referral_earnings (user_id, order_id, from_user_id, level, percent, amount) 
            VALUES ($1, $2, $3, 1, 5, $4)`, referrerID, orderID, userID, earn1)

		// Ищем реферера второго уровня (кто пригласил того кто пригласил)
		var referrer2ID int
		database.DB.QueryRow(
			"SELECT referrer_id FROM referrals WHERE referred_id = $1 AND level = 1",
			referrerID,
		).Scan(&referrer2ID)

		if referrer2ID > 0 {
			// Начисляем 2% рефереру второго уровня
			earn2 := amount * 0.02
			database.DB.Exec("UPDATE referrals SET earnings = earnings + $1 WHERE referrer_id = $2 AND referred_id = $3",
				earn2, referrer2ID, referrerID)
			database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", earn2, referrer2ID)
			database.DB.Exec(`INSERT INTO referral_earnings (user_id, order_id, from_user_id, level, percent, amount) 
                VALUES ($1, $2, $3, 2, 2, $4)`, referrer2ID, orderID, userID, earn2)
		}

		// Уведомление
		notifText := fmt.Sprintf("💰 Реферальный доход: +%.0f ₽ от заказа друга", earn1)
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, '/referral')",
			referrerID, notifText)
	}
}
