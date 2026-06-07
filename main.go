package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"nexus-boost/database"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

// Секреты — загружаются из .env при старте, не хранятся в коде
var (
	vapidPublicKey  string
	vapidPrivateKey string
	adminUsername   string
	adminPassHash   string
	sessionSecret   string
)

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("обязательная переменная окружения не задана: %s", key)
	}
	return v
}

func main() {
	// Загружаем .env (в production переменные задаются через systemd/docker, ошибка игнорируется)
	_ = godotenv.Load()

	vapidPublicKey = mustEnv("VAPID_PUBLIC_KEY")
	vapidPrivateKey = mustEnv("VAPID_PRIVATE_KEY")
	adminUsername = mustEnv("ADMIN_USERNAME")
	adminPassHash = mustEnv("ADMIN_PASSWORD_HASH")
	sessionSecret = mustEnv("SESSION_SECRET")

	// Инициализируем store здесь, после загрузки секрета
	store = sessions.NewCookieStore([]byte(sessionSecret))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}

	database.InitDB()
	initPreparedStatements()

	r := gin.Default()
	r.SetFuncMap(template.FuncMap{
		"list": func(items ...interface{}) []interface{} { return items },
	})
	r.LoadHTMLGlob("templates/*.html")
	r.Static("/static", "./static")

	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "camera=self, microphone=(), geolocation=()")
		c.Next()
	})

	// PWA
	r.GET("/manifest.json", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.File("static/manifest.json")
	})
	r.GET("/sw.js", func(c *gin.Context) {
		c.Header("Content-Type", "application/javascript")
		c.File("static/sw.js")
	})
	r.GET("/offline", func(c *gin.Context) {
		user, _ := c.Get("user")
		c.HTML(http.StatusOK, "layout.html", gin.H{"User": user, "Active": "offline"})
	})

	r.Use(func(c *gin.Context) {
		c.Set("user", getUser(c))
		c.Next()
	})

	r.Use(maintenanceMiddleware)

	r.Use(func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'self'; "+
			"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://kit.fontawesome.com https://cdnjs.cloudflare.com https://cdn.jsdelivr.net; "+
			"style-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://fonts.googleapis.com; "+
			"font-src 'self' https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://fonts.gstatic.com; "+
			"img-src 'self' data: https:; "+
			"connect-src 'self'")
		c.Next()
	})

	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Next()
	})

	var blockedUserAgents = []string{
		"sqlmap", "nikto", "nmap", "masscan", "zgrab",
	}

	r.Use(func(c *gin.Context) {
		ua := strings.ToLower(c.Request.UserAgent())
		for _, blocked := range blockedUserAgents {
			if strings.Contains(ua, blocked) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		}
		c.Next()
	})

	r.Use(func(c *gin.Context) {
		session, _ := store.Get(c.Request, "xsonebmp-session")
		userID, ok := session.Values["user_id"]
		if !ok {
			c.Next()
			return
		}

		uid := userID.(int)
		ip := c.ClientIP()
		ua := c.Request.UserAgent()
		device := getDeviceInfo(ua)

		sessionUUID, _ := session.Values["session_uuid"].(string)
		if sessionUUID == "" {
			sessionUUID = randomString(32)
			session.Values["session_uuid"] = sessionUUID
			session.Save(c.Request, c.Writer)
		}

		go func() {
			loc := getLocationByIP(ip)
			database.DB.Exec(`
            INSERT INTO user_sessions (session_token, user_id, ip, user_agent, location, device, last_seen)
            VALUES ($1, $2, $3, $4, $5, $6, NOW())
            ON CONFLICT (session_token) DO UPDATE 
                SET ip = EXCLUDED.ip,
                    user_agent = EXCLUDED.user_agent,
                    location = EXCLUDED.location,
                    device = EXCLUDED.device,
                    last_seen = NOW()
        `, sessionUUID, uid, ip, ua, loc, device)
		}()

		c.Next()
	})

	r.GET("/sessions", sessionsPage)
	r.POST("/sessions/revoke/:id", revokeSession)

	r.Use(func(c *gin.Context) {
		if ref := c.Query("ref"); ref != "" {
			c.SetCookie("ref", ref, 86400*30, "/", "", false, true)
		}
		c.Next()
	})

	// Аккаунт
	r.GET("/forgot-password", forgotPasswordPage)
	r.POST("/forgot-password", sendResetCode)
	r.GET("/reset-password", resetPasswordPage)
	r.POST("/reset-password", resetPassword)
	r.POST("/profile/change-password", changePassword)

	// Страница покупки PRO
	r.GET("/upgrade-pro", upgradeProPage)
	r.POST("/upgrade-pro", buyPro)
	r.GET("/seller/stats", sellerStatsPage)

	// Страницы
	r.GET("/", homePage)
	r.GET("/marketplace", marketplacePage)
	r.GET("/booster/:id", boosterPage)
	r.GET("/seller/:id", sellerPage)
	r.GET("/profile", profilePage)
	r.GET("/profile/edit", editProfilePage)
	r.POST("/profile/edit", updateProfile)
	r.GET("/achievements", achievementsPage)
	r.GET("/cart", cartPage)
	r.GET("/add-boost", addBoostPage)
	r.POST("/add-boost", addBoost)
	r.GET("/order/:id", orderPage)
	r.POST("/order/:id/send", sendMessage)
	r.POST("/order/:id/rate", rateOrder)
	r.GET("/about", aboutPage)
	r.GET("/contact", contactPage)
	r.POST("/profile/topup", topUpBalance)
	r.POST("/profile/withdraw", withdrawBalance)
	r.GET("/seller/orders", sellerOrdersPage)
	r.POST("/seller/order/:id/status", updateOrderStatus)
	r.GET("/seller/order/:id", sellerOrderDetailPage)
	r.POST("/seller/order/:id/ready", sellerMarkReady)
	r.POST("/seller/order/:id/message", sellerSendMessage)
	r.GET("/boost/:id/edit", editBoostPage)
	r.POST("/boost/:id/edit", editBoost)
	r.GET("/verification", verificationPage)
	r.POST("/verification/apply", applyVerification)

	// Диспуты
	r.GET("/order/:id/dispute", openDisputePage)
	r.POST("/order/:id/dispute", createDispute)
	r.GET("/dispute/:id", disputeDetailPage)
	r.POST("/dispute/:id/message", disputeSendMessage)

	// PRO
	r.GET("/seller/boost/:id", boostItem)
	r.POST("/seller/bulk-create", bulkCreateBoosts)
	r.GET("/seller/insights", sellerInsights)
	r.GET("/seller/templates", sellerTemplates)
	r.POST("/seller/templates/save", saveTemplate)
	r.GET("/seller/template/:id/delete", deleteTemplate)
	r.POST("/seller/template/:id/edit", editTemplate)
	r.GET("/seller/insights/game/:game", sellerInsightsGameDetail)

	// Аутентификация
	r.GET("/register", registerPage)
	r.POST("/register", register)
	r.GET("/login", loginPage)
	r.POST("/login", login)
	r.GET("/logout", logout)
	r.GET("/referral", referralPage)

	// Брендирование профиля
	r.GET("/seller/profile/edit", editSellerProfilePage)
	r.POST("/seller/profile/edit", saveSellerProfile)

	// QR Вход
	r.GET("/qr/scan", func(c *gin.Context) {
		user, _ := c.Get("user")
		c.HTML(http.StatusOK, "layout.html", gin.H{"User": user, "Active": "qr-scan"})
	})
	r.GET("/qr/confirm-page", func(c *gin.Context) {
		user, _ := c.Get("user")
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"User":   user,
			"Active": "qr-confirm",
			"Token":  c.Query("token"),
		})
	})
	r.GET("/qr-generate", func(c *gin.Context) {
		user, _ := c.Get("user")
		c.HTML(http.StatusOK, "layout.html", gin.H{"User": user, "Active": "qr-generate"})
	})
	r.GET("/qr-login", func(c *gin.Context) {
		user, _ := c.Get("user")
		c.HTML(http.StatusOK, "layout.html", gin.H{"User": user, "Active": "qr-login"})
	})
	r.POST("/qr/reject", qrReject)
	r.POST("/qr/scanned", func(c *gin.Context) {
		var req struct {
			Token string `json:"token"`
		}
		c.BindJSON(&req)
		database.DB.Exec("UPDATE qr_sessions SET scanned = true WHERE token = $1", req.Token)
		c.JSON(200, gin.H{"success": true})
	})

	// API
	r.GET("/api/cart", getCart)
	r.POST("/api/cart/add", addToCartAPI)
	r.DELETE("/api/cart/remove/:id", removeFromCart)
	r.DELETE("/api/cart/clear", clearCart)
	r.POST("/api/cart/checkout", checkout)
	r.DELETE("/api/boost/:id/delete", deleteBoost)
	r.GET("/api/order/:id/messages", getMessages)
	r.GET("/api/notifications", getNotificationsHandler)
	r.GET("/api/notifications/count", getNotificationsCountHandler)
	r.POST("/api/notifications/read-all", markAllReadHandler)
	r.POST("/api/notifications/read/:id", markOneReadHandler)
	r.POST("/api/promo/check", checkPromoCode)
	r.GET("/api/discounts", getAvailableDiscounts)
	r.POST("/api/discount/apply", applyDiscount)
	r.POST("/api/escrow/release/:orderId", releaseEscrow)

	r.GET("/qr/generate", qrGenerate)
	r.GET("/qr/check/:token", qrCheckStatus)
	r.POST("/qr/confirm", qrConfirm)

	r.GET("/api/push/key", func(c *gin.Context) {
		c.JSON(200, gin.H{"key": vapidPublicKey})
	})
	r.POST("/api/push/subscribe", pushSubscribe)
	r.POST("/api/push/unsubscribe", func(c *gin.Context) {
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		c.BindJSON(&req)
		if req.Endpoint != "" {
			database.DB.Exec("DELETE FROM push_subscriptions WHERE endpoint = $1", req.Endpoint)
		}
		c.JSON(200, gin.H{"success": true})
	})
	r.POST("/api/push/unsubscribe-all", func(c *gin.Context) {
		session, _ := store.Get(c.Request, "xsonebmp-session")
		userID, ok := session.Values["user_id"]
		if !ok {
			c.JSON(403, gin.H{"error": "auth required"})
			return
		}
		database.DB.Exec("DELETE FROM push_subscriptions WHERE user_id = $1", userID)
		c.JSON(200, gin.H{"success": true})
	})
	r.GET("/api/check-pro", func(c *gin.Context) {
		session, _ := store.Get(c.Request, "xsonebmp-session")
		userID, _ := session.Values["user_id"]
		var isPro bool
		if userID != nil {
			database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", userID).Scan(&isPro)
		}
		c.JSON(200, gin.H{"is_pro": isPro})
	})
	r.GET("/api/dispute/:id/messages", func(c *gin.Context) {
		rows, _ := database.DB.Query(
			"SELECT id, user_id, username, message, is_admin, created_at FROM dispute_messages WHERE dispute_id = $1 ORDER BY created_at ASC",
			c.Param("id"),
		)
		if rows != nil {
			defer rows.Close()
		}
		var msgs []gin.H
		if rows != nil {
			for rows.Next() {
				var id, uid int
				var username, message string
				var isAdmin bool
				var createdAt time.Time
				rows.Scan(&id, &uid, &username, &message, &isAdmin, &createdAt)
				msgs = append(msgs, gin.H{
					"id": id, "user_id": uid, "username": username,
					"message": message, "is_admin": isAdmin, "created_at": createdAt,
				})
			}
		}
		c.JSON(200, msgs)
	})
	r.GET("/api/escrow/refund/:orderId", func(c *gin.Context) {
		session, _ := store.Get(c.Request, "xsonebmp-session")
		userID, _ := session.Values["user_id"]
		orderID := c.Param("orderId")
		var buyerID, sellerID int
		var amount float64
		database.DB.QueryRow("SELECT buyer_id, seller_id, amount FROM escrow_transactions WHERE order_id = $1 AND status = 'frozen'", orderID).Scan(&buyerID, &sellerID, &amount)
		if sellerID != userID {
			c.Redirect(302, "/seller/orders")
			return
		}
		database.DB.Exec("UPDATE escrow_transactions SET status = 'refunded', refunded_at = NOW() WHERE order_id = $1", orderID)
		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", amount, buyerID)
		database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)
		c.Redirect(302, "/seller/order/"+orderID)
	})
	r.GET("/api/ping", func(c *gin.Context) {
		session, _ := store.Get(c.Request, "xsonebmp-session")
		if userID, ok := session.Values["user_id"]; ok {
			database.DB.Exec("INSERT INTO user_online (user_id, last_seen) VALUES ($1, NOW()) ON CONFLICT (user_id) DO UPDATE SET last_seen = NOW()", userID)
			c.JSON(200, gin.H{"ok": true})
			return
		}
		c.JSON(200, gin.H{"ok": false})
	})

	r.GET("/api/review/:id/likes", getReviewLikes)
	r.POST("/api/review/:id/like", toggleReviewLike)

	// Тикеты поддержки
	r.GET("/support", supportPage)
	r.GET("/support/create", createTicketPage)
	r.POST("/support/create", createTicket)
	r.GET("/support/ticket/:id", ticketDetailPage)
	r.POST("/support/ticket/:id/message", ticketSendMessage)
	r.GET("/support/ticket/:id/close", closeTicket)

	// ----------------------------------------------------------------
	// Админка — публичные маршруты (без авторизации)
	// ----------------------------------------------------------------
	r.GET("/admin/login", AdminLoginPage)
	r.POST("/admin/login", AdminLogin)
	r.GET("/admin/logout", AdminLogout)

	// ----------------------------------------------------------------
	// Админка — все защищённые маршруты под middleware
	// ----------------------------------------------------------------
	admin := r.Group("/admin", adminRequired)
	{
		admin.GET("", AdminDashboard)
		admin.GET("/users", AdminUsersPage)
		admin.GET("/orders", AdminOrdersPage)
		admin.POST("/order/:id/status", AdminUpdateOrderStatus)
		admin.GET("/order/:id/delete", AdminDeleteOrder)
		admin.GET("/order/:id", adminOrderDetail)
		admin.GET("/boosts", AdminBoostsPage)
		admin.GET("/boost/:id/delete", AdminDeleteBoost)
		admin.GET("/boost/:id/edit", adminEditBoostPage)
		admin.POST("/boost/:id/edit", adminEditBoost)
		admin.GET("/reviews", adminReviewsPage)
		admin.GET("/review/:id/delete", adminDeleteReview)
		admin.GET("/notify", adminNotifyPage)
		admin.POST("/notify/send", adminSendNotify)
		admin.POST("/user/:id/balance", adminUpdateBalance)
		admin.GET("/user/:id", adminEditUserPage)
		admin.POST("/user/:id", AdminEditUser)
		admin.GET("/messages/:id", adminViewMessages)
		admin.GET("/user/:id/ban", adminBanUser)
		admin.GET("/user/:id/unban", adminUnbanUser)
		admin.GET("/user/:id/make-pro", func(c *gin.Context) {
			userID, _ := strconv.Atoi(c.Param("id"))
			database.DB.Exec("INSERT INTO seller_profiles (user_id, is_pro) VALUES ($1, true) ON CONFLICT (user_id) DO UPDATE SET is_pro = true", userID)
			giveBadge(userID, "pro", "PRO продавец", "👑", "#fbbf24")
			c.Redirect(302, "/admin/users")
		})
		admin.POST("/user/:id/role", adminAssignRole)
		admin.GET("/user/:id/role/remove", adminRemoveRole)
		admin.POST("/user/create", adminCreateUser)
		admin.GET("/search", adminSearch)
		admin.GET("/add-boost", adminAddBoostPage)
		admin.POST("/add-boost", adminAddBoost)
		admin.GET("/export/users", adminExportUsers)
		admin.GET("/stats", adminStatsPage)
		admin.GET("/charts", adminChartsPage)
		admin.GET("/transactions", adminTransactionsPage)
		admin.POST("/refund/:id", adminRefundOrder)
		admin.GET("/logs", adminViewLogs)
		admin.GET("/verify/:id", adminVerifySeller)
		admin.GET("/feature/:id", adminFeatureBoost)
		admin.GET("/promocodes", adminPromocodesPage)
		admin.POST("/promocode/create", adminCreatePromocode)
		admin.GET("/promocode/:id/delete", adminDeletePromocode)
		admin.GET("/promocode/:id/toggle", adminTogglePromocode)
		admin.GET("/sales", adminSalesPage)
		admin.POST("/sale/create", adminCreateSale)
		admin.GET("/sale/:id/toggle", adminToggleSale)
		admin.GET("/sale/:id/delete", adminDeleteSale)
		admin.POST("/first-discount/update", adminUpdateFirstDiscount)
		admin.GET("/disputes", adminDisputesPage)
		admin.GET("/settings", adminSettingsPage)
		admin.POST("/settings", adminUpdateSettings)
		admin.POST("/upload-logo", adminUploadLogo)
		admin.POST("/upload-favicon", adminUploadFavicon)
		admin.POST("/settings/reset", adminResetSettings)
		admin.GET("/verifications", adminVerificationsPage)
		admin.POST("/verification/:id/approve", adminApproveVerification)
		admin.POST("/verification/:id/reject", adminRejectVerification)
		admin.POST("/verification/:id/resolve", adminResolveDispute)
		admin.GET("/tickets", adminTicketsPage)
		admin.GET("/ticket/:id", adminTicketDetail)
		admin.POST("/ticket/:id/message", adminTicketMessage)
		admin.POST("/ticket/:id/status", adminTicketStatus)
	}

	// API для проверки admin (теперь просто возвращает true — middleware уже проверил)
	r.GET("/api/admin/check", adminRequired, func(c *gin.Context) {
		c.JSON(200, gin.H{"is_admin": true})
	})

	// API для верификации продавца
	r.POST("/api/admin/verify/:id", adminRequired, func(c *gin.Context) {
		userID, _ := strconv.Atoi(c.Param("id"))
		giveBadge(userID, "verified", "Верифицированный Продавец", "✅", "#12da97")
		c.JSON(200, gin.H{"success": true})
	})

	r.NoRoute(func(c *gin.Context) {
		user, _ := c.Get("user")
		c.HTML(http.StatusNotFound, "layout.html", gin.H{
			"Active": "404",
			"User":   user,
		})
	})

	r.GET("/maintenance", func(c *gin.Context) {
		c.HTML(http.StatusServiceUnavailable, "layout.html", gin.H{
			"Active":  "maintenance",
			"Message": getMaintenanceMessage(),
		})
	})

	r.GET("/favicon.ico", func(c *gin.Context) {
		c.Status(204)
	})

	fmt.Println("🚀 XSoneBMP запущен на http://localhost:8080")
	r.Run(":8080")
}

// Модели
type User struct {
	ID           int
	Username     string
	Email        string
	Password     string
	Balance      float64
	Level        int
	Orders       int
	SellerOrders int // Добавьте это поле
	Rating       float64
	Reviews      int
	CreatedAt    time.Time
}

type Boost struct {
	ID          int
	Game        string
	Title       string
	Description string
	Price       float64
	BoosterID   int
	Rating      float64
	Reviews     int
	OwnerName   string
	IsPro       bool
}

type CartItem struct {
	ID              int     `json:"id"`
	BoostID         int     `json:"boost_id"`
	Title           string  `json:"title"`
	Price           float64 `json:"price"`
	Game            string  `json:"game"`
	BoosterID       int     `json:"booster_id"`
	Quantity        int     `json:"quantity"`
	PromoCode       string  `json:"promo_code"`
	DiscountPercent int     `json:"discount_percent"`
}
type Order struct {
	ID        int
	UserID    int
	BoostID   int
	BoosterID int
	Title     string
	Game      string
	Booster   string
	Total     float64
	Status    string
	Rated     bool
	CreatedAt time.Time
}

type Message struct {
	ID        int
	OrderID   int
	UserID    int
	Username  string
	Text      string
	CreatedAt time.Time
}

// store инициализируется в main() после загрузки SESSION_SECRET из .env
var store *sessions.CookieStore

var stmtGetUser *sql.Stmt

func initPreparedStatements() {
	var err error
	stmtGetUser, err = database.DB.Prepare(`
        SELECT u.id, u.username, u.email, u.balance, u.level, u.orders,
               COALESCE((SELECT COUNT(*) FROM orders WHERE booster_id = u.id), 0) as seller_orders
        FROM users u WHERE u.id = $1
    `)
	if err != nil {
		log.Printf("❌ Ошибка подготовки stmtGetUser: %v", err)
	}
}

func getUser(c *gin.Context) *User {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		return nil
	}

	// Проверка на отзыв сессии
	sessionUUID, _ := session.Values["session_uuid"].(string)
	if sessionUUID != "" {
		var revoked bool
		err := database.DB.QueryRow("SELECT revoked FROM user_sessions WHERE session_token = $1", sessionUUID).Scan(&revoked)
		if err == nil && revoked {
			// Удаляем куку
			session.Values = make(map[interface{}]interface{})
			session.Save(c.Request, c.Writer)
			return nil
		}
	}
	var isBanned bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM banned_users WHERE user_id = $1)", userID).Scan(&isBanned)
	if isBanned {
		return nil
	}

	var u User

	// Используем подготовленный запрос если есть, иначе обычный
	if stmtGetUser != nil {
		err := stmtGetUser.QueryRow(userID).Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Level, &u.Orders, &u.SellerOrders)
		if err != nil {
			return nil
		}
	} else {
		err := database.DB.QueryRow(`
            SELECT u.id, u.username, u.email, u.balance, u.level, u.orders,
                   COALESCE((SELECT COUNT(*) FROM orders WHERE booster_id = u.id), 0)
            FROM users u WHERE u.id = $1
        `, userID).Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Level, &u.Orders, &u.SellerOrders)
		if err != nil {
			return nil
		}
	}

	return &u
}

func setUser(c *gin.Context, userID int, username string) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	session.Values["user_id"] = userID
	session.Values["username"] = username
	session.Save(c.Request, c.Writer)
}

func render(c *gin.Context, tmpl string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	// НЕ трогаем User если он уже передан
	if _, ok := data["User"]; !ok {
		user, _ := c.Get("user")
		data["User"] = user
	}
	data["Active"] = tmpl
	c.HTML(http.StatusOK, "layout.html", data)
}

// Обработчики страниц
func homePage(c *gin.Context) {
	user, _ := c.Get("user")

	// Используем ТОЛЬКО кеш
	stats := getCachedStats()
	usersCount := stats["users"].(int)
	ordersCount := stats["orders"].(int)
	avgRating := stats["rating"].(float64)

	// Все товары для PRO
	allRows, _ := database.DB.Query(`
    SELECT b.id, b.game, b.title, b.description, b.price, b.rating, b.reviews, b.user_id, u.username,
           COALESCE((SELECT COUNT(*) FROM orders WHERE boost_id = b.id AND status = 'completed'), 0) as sales
    FROM boosts b 
    LEFT JOIN users u ON b.user_id = u.id 
    ORDER BY sales DESC, b.rating DESC
`)
	if allRows != nil {
		defer allRows.Close()
	}

	var allBoosts []Boost
	if allRows != nil {
		for allRows.Next() {
			var b Boost
			var sales int
			allRows.Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price, &b.Rating, &b.Reviews, &b.BoosterID, &b.OwnerName, &sales)
			allBoosts = append(allBoosts, b)
		}
	}

	// PRO товары
	proRows, _ := database.DB.Query(`
        SELECT b.id, b.game, b.title, b.description, b.price, b.rating, b.reviews, b.user_id, u.username
        FROM boosts b 
        JOIN users u ON b.user_id = u.id 
        JOIN seller_profiles sp ON u.id = sp.user_id 
        WHERE sp.is_pro = true
        ORDER BY b.rating DESC LIMIT 4
    `)
	if proRows != nil {
		defer proRows.Close()
	}

	var proBoosts []Boost
	if proRows != nil {
		for proRows.Next() {
			var b Boost
			proRows.Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price, &b.Rating, &b.Reviews, &b.BoosterID, &b.OwnerName)
			proBoosts = append(proBoosts, b)
		}
	}

	// Последние отзывы
	revRows, _ := database.DB.Query(`
    SELECT r.rating, r.text, r.username, r.created_at, o.title as order_title
    FROM reviews r 
    JOIN orders o ON r.order_id = o.id 
    ORDER BY r.created_at DESC LIMIT 10
`)
	if revRows != nil {
		defer revRows.Close()
	}

	type ReviewItem struct {
		Rating     int
		Text       string
		Username   string
		CreatedAt  time.Time
		OrderTitle string
	}

	var latestReviews []ReviewItem
	if revRows != nil {
		for revRows.Next() {
			var r ReviewItem
			revRows.Scan(&r.Rating, &r.Text, &r.Username, &r.CreatedAt, &r.OrderTitle)
			latestReviews = append(latestReviews, r)
		}
	}

	// Топ-4 (исключая PRO)
	var top []Boost
	for _, b := range allBoosts {
		if len(top) < 4 {
			top = append(top, b)
		}
	}

	// Минимальные цены по играм из уже загруженных allBoosts
	gamePrices := make(map[string]float64)
	for _, b := range allBoosts {
		if current, ok := gamePrices[b.Game]; !ok || b.Price < current {
			gamePrices[b.Game] = b.Price
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "home",
		"Data": gin.H{
			"TopBoosts":     top,
			"ProBoosts":     proBoosts,
			"LatestReviews": latestReviews,
			"GamePrices":    gamePrices,
			"Stats": gin.H{
				"Бустеров": fmt.Sprintf("%d", usersCount),
				"Заказов":  fmt.Sprintf("%d", ordersCount),
				"Рейтинг":  fmt.Sprintf("%.1f/5", avgRating),
				"Онлайн":   fmt.Sprintf("%d", usersCount/2+1),
			},
		},
	})
}

func marketplacePage(c *gin.Context) {
	user, _ := c.Get("user")
	rows, _ := database.DB.Query(`
        SELECT b.id, b.game, b.title, b.description, b.price, b.rating, b.reviews, b.user_id, u.username, u.reviews as seller_reviews
        FROM boosts b 
        LEFT JOIN users u ON b.user_id = u.id 
        ORDER BY b.id DESC
    `)
	if rows != nil {
		defer rows.Close()
	}

	var boosts []gin.H
	if rows != nil {
		for rows.Next() {
			var id, userID, reviews, sellerReviews int
			var game, title, desc, ownerName string
			var price, rating float64
			rows.Scan(&id, &game, &title, &desc, &price, &rating, &reviews, &userID, &ownerName, &sellerReviews)
			boosts = append(boosts, gin.H{
				"ID": id, "Game": game, "Title": title, "Description": desc,
				"Price": price, "Rating": rating, "Reviews": reviews,
				"BoosterID": userID, "OwnerName": ownerName,
				"TotalSellerReviews": sellerReviews,
			})
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "marketplace",
		"Boosts": boosts,
	})
}

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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":       &user,
		"Active":     "profile",
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
func cartPage(c *gin.Context)        { render(c, "cart", nil) }
func addBoostPage(c *gin.Context)    { render(c, "add-boost", nil) }

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

func sendMessage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	orderID := c.Param("id")
	text := c.PostForm("message")

	if text != "" {
		database.DB.Exec(
			"INSERT INTO messages (order_id, user_id, username, text) VALUES ($1, $2, $3, $4)",
			orderID, userID, username, text,
		)

		// Получаем booster_id (продавца) этого заказа
		var boosterID int
		err := database.DB.QueryRow("SELECT booster_id FROM orders WHERE id = $1", orderID).Scan(&boosterID)
		if err == nil && boosterID > 0 {
			// Уведомление продавцу
			notifText := fmt.Sprintf("💬 Новое сообщение в заказе #%s от %s", orderID, username)
			database.DB.Exec(
				"INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
				boosterID, notifText, "/seller/order/"+orderID,
			)
			go sendPushNotification(boosterID, "Новое сообщение", notifText, "/seller/order/"+orderID)
		}
	}

	c.Redirect(302, "/order/"+orderID)
}

func rateOrder(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	orderID := c.Param("id")
	rating, _ := strconv.Atoi(c.PostForm("rating"))
	text := c.PostForm("review")

	var o Order
	database.DB.QueryRow("SELECT id, booster_id, rated FROM orders WHERE id = $1 AND user_id = $2", orderID, user.ID).
		Scan(&o.ID, &o.BoosterID, &o.Rated)

	if !o.Rated && rating >= 1 && rating <= 5 {
		// Добавляем отзыв
		database.DB.Exec(
			"INSERT INTO reviews (order_id, user_id, booster_id, username, rating, text) VALUES ($1,$2,$3,$4,$5,$6)",
			o.ID, user.ID, o.BoosterID, user.Username, rating, text,
		)

		// Обновляем рейтинг и количество отзывов у продавца
		var currentRating float64
		var currentReviews int
		database.DB.QueryRow("SELECT COALESCE(rating, 0), COALESCE(reviews, 0) FROM users WHERE id = $1", o.BoosterID).Scan(&currentRating, &currentReviews)

		newRating := (currentRating*float64(currentReviews) + float64(rating)) / float64(currentReviews+1)
		database.DB.Exec("UPDATE users SET rating = $1, reviews = $2, orders = orders + 1 WHERE id = $3", newRating, currentReviews+1, o.BoosterID)

		// Обновляем рейтинг и количество отзывов у товара
		var boostID int
		database.DB.QueryRow("SELECT boost_id FROM orders WHERE id = $1", orderID).Scan(&boostID)

		var boostRating float64
		var boostReviews int
		database.DB.QueryRow("SELECT COALESCE(rating, 0), COALESCE(reviews, 0) FROM boosts WHERE id = $1", boostID).
			Scan(&boostRating, &boostReviews)

		newBoostRating := (boostRating*float64(boostReviews) + float64(rating)) / float64(boostReviews+1)
		database.DB.Exec("UPDATE boosts SET rating = $1, reviews = $2 WHERE id = $3", newBoostRating, boostReviews+1, boostID)

		// Помечаем заказ как оцененный и выполненный
		database.DB.Exec("UPDATE orders SET rated = true, status = 'completed' WHERE id = $1", o.ID)

		// Уведомление продавцу
		notifText := fmt.Sprintf("⭐ Новый отзыв на заказ #%s", orderID)
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
			o.BoosterID, notifText, "/seller/orders")
		go sendPushNotification(o.BoosterID, "Новый отзыв", notifText, "/seller/orders")
	}

	c.Redirect(302, "/order/"+orderID)
}

func aboutPage(c *gin.Context) {
	user, _ := c.Get("user")

	var usersCount, ordersCount, boostsCount, gamesCount int
	var avgRating float64

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&ordersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM boosts").Scan(&boostsCount)
	database.DB.QueryRow("SELECT COUNT(DISTINCT game) FROM boosts WHERE game IS NOT NULL AND game != ''").Scan(&gamesCount)
	database.DB.QueryRow("SELECT COALESCE(AVG(rating), 0) FROM users WHERE rating > 0").Scan(&avgRating)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "about",
		"Data": gin.H{
			"UsersCount":  usersCount,
			"OrdersCount": ordersCount,
			"BoostsCount": boostsCount,
			"GamesCount":  gamesCount,
			"Рейтинг":     avgRating,
		},
	})
}

func contactPage(c *gin.Context) {
	user, _ := c.Get("user")

	var usersCount int

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "contact",
		"Data": gin.H{
			"UsersCount": usersCount,
		},
	})
}

// Аутентификация
func registerPage(c *gin.Context) {
	// Генерируем CSRF токен и сохраняем в сессии
	session, _ := store.Get(c.Request, "xsonebmp-session")
	csrfToken := randomString(32)
	session.Values["csrf_token"] = csrfToken
	session.Save(c.Request, c.Writer)

	render(c, "register", gin.H{
		"csrf": csrfToken,
	})
}
func isStrongPassword(pw string) bool {
	if len(pw) < 8 {
		return false
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, ch := range pw {
		switch {
		case unicode.IsUpper(ch):
			hasUpper = true
		case unicode.IsLower(ch):
			hasLower = true
		case unicode.IsDigit(ch):
			hasDigit = true
		case unicode.IsPunct(ch) || unicode.IsSymbol(ch):
			hasSpecial = true
		}
	}
	return hasUpper && hasLower && hasDigit && hasSpecial
}
func register(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")

	// 1. Проверка CSRF токена
	expectedToken, ok := session.Values["csrf_token"].(string)
	if !ok || expectedToken == "" || expectedToken != c.PostForm("csrf_token") {
		render(c, "register", gin.H{"Error": "Ошибка безопасности. Обновите страницу."})
		return
	}

	// 2. Проверка honeypot поля
	if c.PostForm("website") != "" {
		// Молча перенаправляем или показываем ошибку
		c.Redirect(302, "/")
		return
	}

	// 3. Rate limiting по IP
	ip := c.ClientIP()
	loginMu.Lock()
	attempts, exists := loginAttempts[ip] // Используем ту же мапу, что и для логина, но можно отдельную
	now := time.Now()
	if exists {
		valid := []time.Time{}
		for _, t := range attempts {
			if now.Sub(t) < 1*time.Hour {
				valid = append(valid, t)
			}
		}
		if len(valid) >= 3 {
			loginMu.Unlock()
			render(c, "register", gin.H{"Error": "Слишком много попыток. Попробуйте позже."})
			return
		}
		valid = append(valid, now)
		loginAttempts[ip] = valid
	} else {
		loginAttempts[ip] = []time.Time{now}
	}
	loginMu.Unlock()

	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.TrimSpace(c.PostForm("email"))
	password := c.PostForm("password")

	// 4. Валидация полей
	if len(username) < 3 || len(username) > 30 {
		render(c, "register", gin.H{"Error": "Имя должно быть от 3 до 30 символов"})
		return
	}
	if !isValidEmail(email) {
		render(c, "register", gin.H{"Error": "Некорректный email"})
		return
	}
	if !isValidDomain(email) {
		render(c, "register", gin.H{"Error": "Некорректный email домен"})
		return
	}
	if len(password) < 8 || !containsLetterAndDigit(password) {
		render(c, "register", gin.H{"Error": "Пароль должен быть от 8 символов и содержать буквы и цифры"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		render(c, "register", gin.H{"Error": "Ошибка сервера"})
		return
	}

	var userID int
	err = database.DB.QueryRow(
		"INSERT INTO users (username, email, password, balance) VALUES ($1, $2, $3, 10000) RETURNING id",
		username, email, string(hash),
	).Scan(&userID)

	if err != nil {
		// Общее сообщение, чтобы не раскрывать существование пользователя
		render(c, "register", gin.H{"Error": "Ошибка регистрации. Попробуйте другие данные."})
		return
	}
	database.DB.Exec("DELETE FROM user_sessions WHERE user_id = $1", userID)

	// Генерация реферального кода
	refCode := randomString(8)
	database.DB.Exec("UPDATE users SET referral_code = $1 WHERE id = $2", refCode, userID)

	if refCookie, err := c.Cookie("ref"); err == nil && refCookie != "" {
		var referrerID int
		err := database.DB.QueryRow("SELECT id FROM users WHERE referral_code = $1", refCookie).Scan(&referrerID)
		if err == nil && referrerID > 0 && referrerID != userID {
			database.DB.Exec(
				"INSERT INTO referrals (referrer_id, referred_id, code) VALUES ($1, $2, $3)",
				referrerID, userID, refCookie,
			)
		}
	}

	// Очищаем CSRF токен и устанавливаем сессию
	delete(session.Values, "csrf_token")
	session.Values["user_id"] = userID
	session.Values["username"] = username
	session.Values["session_uuid"] = randomString(32) // или uuid.New().String()
	session.Save(c.Request, c.Writer)

	c.Redirect(302, "/profile")
}
func isValidDomain(email string) bool {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	domain := parts[1]

	// Проверка на локальный хост
	if domain == "localhost" || domain == "127.0.0.1" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mxRecords, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err != nil || len(mxRecords) == 0 {
		_, err := net.DefaultResolver.LookupHost(ctx, domain)
		return err == nil
	}
	return true
}
func isValidEmail(email string) bool {
	// Минимальная длина, наличие @, правильный формат
	if len(email) < 5 || !strings.Contains(email, "@") {
		return false
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	return re.MatchString(email)
}

func containsLetterAndDigit(s string) bool {
	hasLetter := false
	hasDigit := false
	for _, c := range s {
		if unicode.IsLetter(c) {
			hasLetter = true
		}
		if unicode.IsDigit(c) {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

var loginAttempts = make(map[string][]time.Time)
var loginMu sync.Mutex

func loginPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	csrfToken := randomString(32)
	session.Values["csrf_token"] = csrfToken
	session.Save(c.Request, c.Writer)

	render(c, "login", gin.H{
		"csrf": csrfToken,
	})
}
func login(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")

	// 1. CSRF
	expectedToken, ok := session.Values["csrf_token"].(string)
	if !ok || expectedToken == "" || expectedToken != c.PostForm("csrf_token") {
		render(c, "login", gin.H{"Error": "Ошибка безопасности. Обновите страницу."})
		return
	}

	// 2. Honeypot
	if c.PostForm("website") != "" {
		c.Redirect(302, "/")
		return
	}

	login := strings.TrimSpace(c.PostForm("username"))
	password := c.PostForm("password")
	ip := c.ClientIP()

	// Rate limiting
	loginMu.Lock()
	attempts, exists := loginAttempts[ip]
	now := time.Now()
	var valid []time.Time
	if exists {
		for _, t := range attempts {
			if now.Sub(t) < 15*time.Minute {
				valid = append(valid, t)
			}
		}
	}
	if len(valid) >= 5 {
		loginMu.Unlock()
		render(c, "login", gin.H{"Error": "Слишком много попыток. Подождите 15 минут."})
		return
	}
	valid = append(valid, now)
	loginAttempts[ip] = valid
	loginMu.Unlock()

	var u User
	err := database.DB.QueryRow(
		"SELECT id, username, password FROM users WHERE username = $1 OR email = $1",
		login,
	).Scan(&u.ID, &u.Username, &u.Password)

	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) != nil {
		render(c, "login", gin.H{"Error": "Неверный логин/email или пароль"})
		return
	}

	var isBanned bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM banned_users WHERE user_id = $1)", u.ID).Scan(&isBanned)
	if isBanned {
		render(c, "login", gin.H{"Error": "Ваш аккаунт заблокирован"})
		return
	}

	// Удаляем старые сессии и создаём новую
	database.DB.Exec("DELETE FROM user_sessions WHERE user_id = $1", u.ID)
	session.Values["session_uuid"] = randomString(32)
	session.Values["user_id"] = u.ID
	session.Values["username"] = u.Username
	delete(session.Values, "csrf_token") // очищаем CSRF токен
	session.Save(c.Request, c.Writer)

	c.Redirect(302, "/profile")
}

func logout(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")

	if userID, ok := session.Values["user_id"]; ok {
		database.DB.Exec("UPDATE user_online SET last_seen = NOW() - INTERVAL '10 minutes' WHERE user_id = $1", userID)
	}

	session.Values = make(map[interface{}]interface{})
	session.Save(c.Request, c.Writer)
	c.Redirect(302, "/")

}

// API (сокращено для brevity)
func getCart(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	if user == nil {
		c.JSON(200, gin.H{"items": []CartItem{}, "total": 0})
		return
	}

	rows, _ := database.DB.Query("SELECT id, boost_id, title, price, game FROM cart_items WHERE user_id = $1", user.ID)
	if rows != nil {
		defer rows.Close()
	}

	var items []CartItem
	total := 0.0
	if rows != nil {
		for rows.Next() {
			var ci CartItem
			rows.Scan(&ci.ID, &ci.BoostID, &ci.Title, &ci.Price, &ci.Game)
			ci.Quantity = 1
			items = append(items, ci)
			total += ci.Price
		}
	}
	if items == nil {
		items = []CartItem{}
	}

	c.JSON(200, gin.H{"items": items, "total": total})
}

func addToCartAPI(c *gin.Context) {
	u, _ := c.Get("user")
	if u == nil {
		c.JSON(403, gin.H{"success": false, "message": "Требуется авторизация"})
		return
	}
	user := u.(*User)

	var item CartItem
	if err := json.NewDecoder(c.Request.Body).Decode(&item); err != nil {
		c.JSON(400, gin.H{"success": false, "message": "Неверные данные"})
		return
	}

	// Проверяем, не свой ли товар
	var ownerID int
	err := database.DB.QueryRow("SELECT user_id FROM boosts WHERE id = $1", item.BoostID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"success": false, "message": "Товар не найден"})
		return
	}
	if ownerID == user.ID {
		c.JSON(400, gin.H{"success": false, "message": "Нельзя добавить в корзину свой товар"})
		return
	}

	// Проверяем, есть ли уже этот товар в корзине
	var count int
	err = database.DB.QueryRow(
		"SELECT COUNT(*) FROM cart_items WHERE user_id = $1 AND boost_id = $2",
		user.ID, item.BoostID,
	).Scan(&count)

	if count > 0 {
		var totalCount int
		database.DB.QueryRow("SELECT COUNT(*) FROM cart_items WHERE user_id = $1", user.ID).Scan(&totalCount)
		c.JSON(200, gin.H{"success": true, "cartCount": totalCount, "message": "Этот товар уже в корзине"})
		return
	}

	// Добавляем новый товар
	_, err = database.DB.Exec(
		"INSERT INTO cart_items (user_id, boost_id, title, price, game, booster_id) VALUES ($1,$2,$3,$4,$5,$6)",
		user.ID, item.BoostID, item.Title, item.Price, item.Game, item.BoosterID,
	)
	if err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}

	var totalCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM cart_items WHERE user_id = $1", user.ID).Scan(&totalCount)

	c.JSON(200, gin.H{"success": true, "cartCount": totalCount, "message": "Добавлено в корзину!"})
}

func removeFromCart(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	database.DB.Exec("DELETE FROM cart_items WHERE id=$1 AND user_id=$2", c.Param("id"), user.ID)
	c.JSON(200, gin.H{"success": true})
}

func clearCart(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	database.DB.Exec("DELETE FROM cart_items WHERE user_id=$1", user.ID)
	c.JSON(200, gin.H{"success": true})
}

func checkout(c *gin.Context) {
	u, _ := c.Get("user")
	if u == nil {
		c.JSON(200, gin.H{"success": false, "message": "Требуется авторизация"})
		return
	}
	user := u.(*User)

	rows, _ := database.DB.Query("SELECT boost_id, title, price, game, booster_id FROM cart_items WHERE user_id = $1", user.ID)
	if rows != nil {
		defer rows.Close()
	}

	total := 0.0
	var items []struct {
		bid, bid2 int
		t, g      string
		p         float64
	}

	if rows != nil {
		for rows.Next() {
			var it struct {
				bid, bid2 int
				t, g      string
				p         float64
			}
			rows.Scan(&it.bid, &it.t, &it.p, &it.g, &it.bid2)
			items = append(items, it)
			total += it.p
		}
	}

	if len(items) == 0 {
		c.JSON(200, gin.H{"success": false, "message": "Корзина пуста"})
		return
	}

	// Транзакция для атомарного списания
	tx, err := database.DB.Begin()
	if err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	defer tx.Rollback()

	var bal float64
	tx.QueryRow("SELECT balance FROM users WHERE id = $1 FOR UPDATE", user.ID).Scan(&bal)

	if bal < total {
		c.JSON(200, gin.H{"success": false, "message": fmt.Sprintf("Недостаточно средств! Нужно %.0f ₽, у вас %.0f ₽", total, bal)})
		return
	}

	balanceBefore := bal
	var lastOrderID int

	for _, it := range items {
		err := tx.QueryRow(
			"INSERT INTO orders (user_id, boost_id, booster_id, title, game, total) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id",
			user.ID, it.bid, it.bid2, it.t, it.g, it.p,
		).Scan(&lastOrderID)

		if err == nil {
			tx.Exec("INSERT INTO escrow_transactions (order_id, buyer_id, seller_id, amount) VALUES ($1,$2,$3,$4)",
				lastOrderID, user.ID, it.bid2, it.p)

			// ... остальные вставки (сообщения, уведомления) тоже через tx ...
		}
	}

	tx.Exec("UPDATE users SET balance = balance - $1, orders = orders + $2 WHERE id = $3", total, len(items), user.ID)
	tx.Exec("DELETE FROM cart_items WHERE user_id = $1", user.ID)

	var newBal float64
	tx.QueryRow("SELECT balance FROM users WHERE id = $1", user.ID).Scan(&newBal)
	tx.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'payment',$2,$3,$4,'Оплата заказа',$5)",
		user.ID, total, balanceBefore, newBal, lastOrderID)

	tx.Commit()

	go processReferralEarnings(user.ID, lastOrderID, total)

	c.JSON(200, gin.H{
		"success":    true,
		"message":    fmt.Sprintf("Заказ #%d оформлен! -%.0f ₽", lastOrderID, total),
		"newBalance": newBal,
		"orderID":    lastOrderID,
	})
}

func deleteBoost(c *gin.Context) {
	u, _ := c.Get("user")
	user := u.(*User)
	var oid int
	database.DB.QueryRow("SELECT user_id FROM boosts WHERE id=$1", c.Param("id")).Scan(&oid)
	if oid == user.ID {
		database.DB.Exec("DELETE FROM boosts WHERE id=$1", c.Param("id"))
		c.JSON(200, gin.H{"success": true})
	} else {
		c.JSON(200, gin.H{"success": false})
	}
}

func getMessages(c *gin.Context) {
	rows, _ := database.DB.Query("SELECT username, text, created_at, user_id FROM messages WHERE order_id=$1 ORDER BY created_at ASC", c.Param("id"))
	if rows != nil {
		defer rows.Close()
	}
	var msgs []gin.H
	if rows != nil {
		for rows.Next() {
			var u, t string
			var ct time.Time
			var uid int
			rows.Scan(&u, &t, &ct, &uid)
			msgs = append(msgs, gin.H{"username": u, "text": t, "created_at": ct, "user_id": uid})
		}
	}
	c.JSON(200, msgs)
}

func topUpBalance(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	database.DB.Exec("UPDATE users SET balance=balance+$1 WHERE id=$2", amount, userID)
	c.Redirect(302, "/profile?success=Баланс+пополнен+на+"+fmt.Sprintf("%.0f", amount)+"+₽")
}

func withdrawBalance(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	var bal float64
	database.DB.QueryRow("SELECT balance FROM users WHERE id=$1", userID).Scan(&bal)
	if amount > bal {
		c.Redirect(302, "/profile?error=Недостаточно+средств")
		return
	}
	database.DB.Exec("UPDATE users SET balance=balance-$1 WHERE id=$2", amount, userID)
	c.Redirect(302, "/profile?success=Выведено+"+fmt.Sprintf("%.0f", amount)+"+₽")
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
	c.HTML(http.StatusOK, "layout.html", gin.H{"User": &user, "Active": "seller-orders", "Data": gin.H{"Orders": orders}})
}

func updateOrderStatus(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("id")
	status := c.PostForm("status")

	var boosterID int
	database.DB.QueryRow("SELECT booster_id FROM orders WHERE id = $1", orderID).Scan(&boosterID)

	if boosterID == userID {
		database.DB.Exec("UPDATE orders SET status = $1 WHERE id = $2", status, orderID)

		// Получаем buyerID и username покупателя
		var buyerID int
		var buyerUsername string
		err := database.DB.QueryRow(
			"SELECT u.id, u.username FROM users u JOIN orders o ON u.id = o.user_id WHERE o.id = $1",
			orderID,
		).Scan(&buyerID, &buyerUsername)

		if err == nil && buyerID > 0 {
			// Уведомление покупателю
			notifText := fmt.Sprintf("📦 Статус заказа #%s изменен на: %s", orderID, status)
			database.DB.Exec(
				"INSERT INTO notifications (user_id, text, link) VALUES ($1, $2, $3)",
				buyerID, notifText, "/order/"+orderID,
			)
			go sendPushNotification(buyerID, "Статус заказа", notifText, "/order/"+orderID)
		}
	}

	c.Redirect(302, "/seller/orders")
}

// Админка
func isAdmin(c *gin.Context) bool {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		return false
	}

	// Админы: пользователь с ID=1 (или добавьте другие ID)
	return userID == 1
}

func AdminLoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{"Title": "XSoneBMP Админ", "Active": "admin-login"})
}

func AdminLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	// Сравниваем username через hmac.Equal — защита от timing attack
	usernameOK := hmac.Equal([]byte(username), []byte(adminUsername))
	// Сравниваем пароль через bcrypt
	passOK := bcrypt.CompareHashAndPassword([]byte(adminPassHash), []byte(password)) == nil

	if usernameOK && passOK {
		session, _ := store.Get(c.Request, "xsonebmp-session")
		session.Values["admin_id"] = 1
		session.Save(c.Request, c.Writer)
		c.Redirect(302, "/admin")
		return
	}

	// Фиксированная задержка при ошибке — усложняет брутфорс
	time.Sleep(300 * time.Millisecond)
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Title":  "XSoneBMP Админ",
		"Active": "admin-login",
		"Error":  "Неверный логин или пароль",
	})
}

// adminRequired — middleware, проверяет наличие admin сессии
func adminRequired(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	if _, ok := session.Values["admin_id"]; !ok {
		c.Redirect(http.StatusFound, "/admin/login")
		c.Abort()
		return
	}
	c.Next()
}

func AdminDashboard(c *gin.Context) {
	user, _ := c.Get("user")

	var usersCount, boostsCount, ordersCount int
	var totalEarnings float64

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM boosts").Scan(&boostsCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&ordersCount)
	database.DB.QueryRow("SELECT COALESCE(SUM(total), 0) FROM orders WHERE status = 'completed'").Scan(&totalEarnings)

	// Данные для графика
	rows, _ := database.DB.Query(`
        SELECT TO_CHAR(created_at, 'YYYY-MM') as month, COALESCE(SUM(total), 0)
        FROM orders WHERE status = 'completed' 
        AND created_at >= NOW() - INTERVAL '12 months'
        GROUP BY month ORDER BY month
    `)
	defer rows.Close()

	type ChartItem struct {
		Date    string
		Total   float64
		Percent int
	}
	var chartData []ChartItem
	var maxVal float64
	// Сначала собираем все данные
	for rows.Next() {
		var item ChartItem
		rows.Scan(&item.Date, &item.Total)
		chartData = append(chartData, item)
		if item.Total > maxVal {
			maxVal = item.Total
		}
	}
	// Затем вычисляем проценты
	for i := range chartData {
		if maxVal > 0 {
			chartData[i].Percent = int(chartData[i].Total * 100 / maxVal)
		}
	}

	// Последние заказы
	orderRows, _ := database.DB.Query(`
        SELECT o.id, o.title, o.total, o.status, u.username
        FROM orders o LEFT JOIN users u ON o.user_id = u.id
        ORDER BY o.created_at DESC LIMIT 10
    `)
	defer orderRows.Close()

	type RecentOrder struct {
		ID       int
		Title    string
		Total    float64
		Status   string
		Username string
	}
	var recentOrders []RecentOrder
	for orderRows.Next() {
		var o RecentOrder
		orderRows.Scan(&o.ID, &o.Title, &o.Total, &o.Status, &o.Username)
		recentOrders = append(recentOrders, o)
	}

	// Последние пользователи
	userRows, _ := database.DB.Query("SELECT id, username, email, balance, orders FROM users ORDER BY id DESC LIMIT 10")
	defer userRows.Close()

	type RecentUser struct {
		ID       int
		Username string
		Email    string
		Balance  float64
		Orders   int
	}
	var recentUsers []RecentUser
	for userRows.Next() {
		var u RecentUser
		userRows.Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Orders)
		recentUsers = append(recentUsers, u)
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "admin",
		"Data": gin.H{
			"UsersCount":    usersCount,
			"BoostsCount":   boostsCount,
			"OrdersCount":   ordersCount,
			"TotalEarnings": totalEarnings,
			"ChartData":     chartData,
			"RecentOrders":  recentOrders,
			"RecentUsers":   recentUsers,
		},
	})
}

func AdminLogout(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	session.Values["admin_id"] = nil
	session.Save(c.Request, c.Writer)
	c.Redirect(302, "/admin/login")
}

func AdminUsersPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
    SELECT u.id, u.username, u.email, u.balance, u.orders, u.rating, 
           CASE WHEN b.user_id IS NOT NULL THEN true ELSE false END as banned
    FROM users u 
    LEFT JOIN banned_users b ON u.id = b.user_id 
    ORDER BY u.id DESC
`)
	defer rows.Close()

	user, _ := c.Get("user")

	type UserRow struct {
		ID       int
		Username string
		Email    string
		Balance  float64
		Orders   int
		Rating   float64
		Banned   bool
	}

	var users []UserRow
	for rows.Next() {
		var u UserRow
		rows.Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Orders, &u.Rating, &u.Banned)
		users = append(users, u)
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-users",
		"User":   user,
		"Data":   gin.H{"Users": users},
	})
}

func AdminEditUser(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}

	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.Redirect(302, "/admin/users?error=Неверный+ID")
		return
	}

	username := c.PostForm("username")
	email := c.PostForm("email")
	balance, _ := strconv.ParseFloat(c.PostForm("balance"), 64)

	database.DB.Exec(
		"UPDATE users SET username=$1, email=$2, balance=$3 WHERE id=$4",
		username, email, balance, userID,
	)

	c.Redirect(302, "/admin/users?success=Пользователь+обновлён")
}

func AdminDeleteUser(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("DELETE FROM users WHERE id=$1", c.Param("id"))
	c.Redirect(302, "/admin/users")
}

func AdminOrdersPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT o.id, u.username, o.title, o.game, o.total, o.status, o.created_at 
        FROM orders o 
        LEFT JOIN users u ON o.user_id = u.id 
        ORDER BY o.created_at DESC
    `)
	if rows != nil {
		defer rows.Close()
	}
	user, _ := c.Get("user")

	type OrderRow struct {
		ID        int
		Username  string
		Title     string
		Game      string
		Total     float64
		Status    string
		CreatedAt time.Time
	}

	var orders []OrderRow
	if rows != nil {
		for rows.Next() {
			var o OrderRow
			rows.Scan(&o.ID, &o.Username, &o.Title, &o.Game, &o.Total, &o.Status, &o.CreatedAt)
			orders = append(orders, o)
		}
	}
	if orders == nil {
		orders = []OrderRow{}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-orders",
		"User":   user,
		"Data":   gin.H{"Orders": orders},
	})
}

func AdminUpdateOrderStatus(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("UPDATE orders SET status=$1 WHERE id=$2", c.PostForm("status"), c.Param("id"))
	c.Redirect(302, "/admin/orders")
}

func AdminDeleteOrder(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("DELETE FROM orders WHERE id=$1", c.Param("id"))
	c.Redirect(302, "/admin/orders")
}

func AdminBoostsPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT b.id, b.game, b.title, b.price, b.rating, b.reviews, COALESCE(u.username, 'Нет') 
        FROM boosts b 
        LEFT JOIN users u ON b.user_id = u.id 
        ORDER BY b.id DESC
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type BoostRow struct {
		ID      int
		Game    string
		Title   string
		Price   float64
		Rating  float64
		Reviews int
		Owner   string
	}

	var boosts []BoostRow
	if rows != nil {
		for rows.Next() {
			var b BoostRow
			rows.Scan(&b.ID, &b.Game, &b.Title, &b.Price, &b.Rating, &b.Reviews, &b.Owner)
			boosts = append(boosts, b)
		}
	}
	if boosts == nil {
		boosts = []BoostRow{}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-boosts",
		"User":   user,
		"Data":   gin.H{"Boosts": boosts},
	})
}

func AdminDeleteBoost(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}
	database.DB.Exec("DELETE FROM boosts WHERE id=$1", c.Param("id"))
	c.Redirect(302, "/admin/boosts")
}

func getNotificationsHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(200, []gin.H{})
		return
	}

	rows, _ := database.DB.Query(
		"SELECT id, text, link, is_read, created_at FROM notifications WHERE user_id = $1 AND is_read = false ORDER BY created_at DESC LIMIT 20",
		userID,
	)
	if rows != nil {
		defer rows.Close()
	}

	var notifs []gin.H
	if rows != nil {
		for rows.Next() {
			var id int
			var text, link string
			var isRead bool
			var createdAt time.Time
			rows.Scan(&id, &text, &link, &isRead, &createdAt)
			notifs = append(notifs, gin.H{"id": id, "text": text, "link": link, "created_at": createdAt})
		}
	}

	c.JSON(200, notifs)
}

func getNotificationsCountHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(200, gin.H{"count": 0})
		return
	}

	var count int
	database.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND is_read = false", userID).Scan(&count)
	c.JSON(200, gin.H{"count": count})
}

func markAllReadHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"success": false})
		return
	}

	database.DB.Exec("UPDATE notifications SET is_read = true WHERE user_id = $1", userID)
	c.JSON(200, gin.H{"success": true})
}

func markOneReadHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"success": false})
		return
	}

	notifID := c.Param("id")
	database.DB.Exec("UPDATE notifications SET is_read = true WHERE id = $1 AND user_id = $2", notifID, userID)
	c.JSON(200, gin.H{"success": true})
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "seller-order",
		"IsPro":  isPro,
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
	userID, _ := session.Values["user_id"]

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	orderID := c.Param("id")
	text := c.PostForm("message")

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
func orderPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username, email, balance FROM users WHERE id = $1", userID).
		Scan(&user.ID, &user.Username, &user.Email, &user.Balance)

	orderID := c.Param("id")

	var o Order
	var boosterName string
	err := database.DB.QueryRow(
		`SELECT o.id, o.user_id, o.boost_id, o.booster_id, o.title, o.game, o.total, o.status, o.rated, o.created_at, u.username 
         FROM orders o 
         LEFT JOIN users u ON o.booster_id = u.id 
         WHERE o.id = $1 AND o.user_id = $2`,
		orderID, userID,
	).Scan(&o.ID, &o.UserID, &o.BoostID, &o.BoosterID, &o.Title, &o.Game, &o.Total, &o.Status, &o.Rated, &o.CreatedAt, &boosterName)

	if err != nil {
		c.String(404, "Заказ не найден")
		return
	}

	o.Booster = boosterName

	// Сообщения чата
	rows, _ := database.DB.Query(
		"SELECT id, user_id, username, text, created_at FROM messages WHERE order_id = $1 ORDER BY created_at ASC",
		orderID,
	)
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

	// Проверяем есть ли спор по этому заказу
	var disputeID int
	database.DB.QueryRow("SELECT COALESCE((SELECT id FROM disputes WHERE order_id = $1), 0)", orderID).Scan(&disputeID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "order",
		"Data": gin.H{
			"Order":     o,
			"Messages":  messages,
			"DisputeID": disputeID,
		},
	})
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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
func adminReviewsPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT r.id, r.rating, r.text, r.username, r.created_at, o.title 
        FROM reviews r 
        JOIN orders o ON r.order_id = o.id 
        ORDER BY r.created_at DESC
    `)
	defer rows.Close()

	user, _ := c.Get("user")

	type ReviewRow struct {
		ID        int
		Rating    int
		Text      string
		Username  string
		CreatedAt time.Time
		Title     string
	}

	var reviews []ReviewRow
	for rows.Next() {
		var r ReviewRow
		rows.Scan(&r.ID, &r.Rating, &r.Text, &r.Username, &r.CreatedAt, &r.Title)
		reviews = append(reviews, r)
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-reviews",
		"User":   user,
		"Data":   gin.H{"Reviews": reviews},
	})
}

func adminDeleteReview(c *gin.Context) {
	database.DB.Exec("DELETE FROM reviews WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/reviews")
}

// Уведомления всем пользователям
func adminNotifyPage(c *gin.Context) {
	user, _ := c.Get("user")

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "admin-notify",
	})
}

func adminSendNotify(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]
	message := c.PostForm("message")
	if message != "" {
		database.DB.Exec(
			"INSERT INTO notifications (user_id, text, link) SELECT id, $1, '' FROM users",
			message,
		)
		go func() {
			rows, _ := database.DB.Query("SELECT DISTINCT user_id FROM push_subscriptions")
			if rows != nil {
				defer rows.Close()
				for rows.Next() {
					var uid int
					rows.Scan(&uid)
					sendPushNotification(uid, "XSoneBMP", message, "/")
				}
			}
		}()
	}

	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'mass_notify', $2)", userID, message)

	c.Redirect(302, "/admin?success=Уведомления+отправлены")
}

// Управление балансом
func adminUpdateBalance(c *gin.Context) {
	userID := c.Param("id")
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	operation := c.PostForm("operation")

	if operation == "add" {
		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", amount, userID)
	} else if operation == "set" {
		database.DB.Exec("UPDATE users SET balance = $1 WHERE id = $2", amount, userID)
	}

	c.Redirect(302, "/admin/users")
}

// Бан/разбан пользователя
func adminBanUser(c *gin.Context) {
	userID := c.Param("id")
	database.DB.Exec("INSERT INTO banned_users (user_id, reason) VALUES ($1, 'Нарушение правил') ON CONFLICT DO NOTHING", userID)
	c.Redirect(302, "/admin/users")
	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'ban', 'Забанил пользователя #'+$2)", userID, c.Param("id"))

}

func adminUnbanUser(c *gin.Context) {
	database.DB.Exec("DELETE FROM banned_users WHERE user_id = $1", c.Param("id"))
	c.Redirect(302, "/admin/users")
}

// Поиск по всем таблицам
func adminSearch(c *gin.Context) {
	user, _ := c.Get("user")
	query := c.Query("q")
	if query == "" {
		// Передаём пустые результаты и сам запрос
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"Active": "admin-search",
			"User":   user,
			"Data": gin.H{
				"Users":  []gin.H{},
				"Orders": []gin.H{},
				"Boosts": []gin.H{},
				"Query":  query,
			},
		})
		return
	}

	searchPattern := "%" + query + "%"

	// Безопасный параметризованный запрос
	uRows, err := database.DB.Query(
		"SELECT id, username, email FROM users WHERE username ILIKE $1 OR email ILIKE $1 LIMIT 10",
		searchPattern,
	)
	if err != nil {
		log.Printf("❌ Search users error: %v", err)
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"Error": "Search failed"})
		return
	}
	defer uRows.Close()

	var users []gin.H
	var orders []gin.H
	var boosts []gin.H

	if query != "" {
		searchPattern := "%" + query + "%"

		uRows, _ := database.DB.Query(
			"SELECT id, username, email FROM users WHERE username ILIKE $1 OR email ILIKE $1 LIMIT 10",
			searchPattern,
		)
		if uRows != nil {
			defer uRows.Close()
			for uRows.Next() {
				var id int
				var name, email string
				uRows.Scan(&id, &name, &email)
				users = append(users, gin.H{"id": id, "name": name, "email": email})
			}
		}

		oRows, _ := database.DB.Query(
			"SELECT o.id, u.username, o.title, o.total FROM orders o JOIN users u ON o.user_id = u.id WHERE o.title ILIKE $1 LIMIT 10",
			searchPattern,
		)
		if oRows != nil {
			defer oRows.Close()
			for oRows.Next() {
				var id int
				var username, title string
				var total float64
				oRows.Scan(&id, &username, &title, &total)
				orders = append(orders, gin.H{"id": id, "username": username, "title": title, "total": total})
			}
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-search",
		"User":   user,
		"Data":   gin.H{"Users": users, "Orders": orders, "Boosts": boosts, "Query": query},
	})
}

// Детали заказа
func adminOrderDetail(c *gin.Context) {
	orderID := c.Param("id")
	user, _ := c.Get("user")

	var o Order
	var buyerName string
	err := database.DB.QueryRow(`
        SELECT o.id, o.user_id, o.boost_id, o.booster_id, o.title, o.game, 
               o.total, o.status, o.created_at, u.username
        FROM orders o 
        LEFT JOIN users u ON o.user_id = u.id 
        WHERE o.id = $1
    `, orderID).Scan(&o.ID, &o.UserID, &o.BoostID, &o.BoosterID, &o.Title, &o.Game,
		&o.Total, &o.Status, &o.CreatedAt, &buyerName)

	if err != nil {
		c.String(404, "Заказ не найден")
		return
	}

	// Получаем имя продавца
	var boosterName string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", o.BoosterID).Scan(&boosterName)
	o.Booster = boosterName

	// Сообщения
	rows, _ := database.DB.Query(
		"SELECT id, username, text, created_at FROM messages WHERE order_id = $1 ORDER BY created_at ASC",
		orderID,
	)
	if rows != nil {
		defer rows.Close()
	}
	var messages []Message
	if rows != nil {
		for rows.Next() {
			var m Message
			rows.Scan(&m.ID, &m.Username, &m.Text, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "admin-order-detail",
		"Data": gin.H{
			"Order": gin.H{
				"ID":        o.ID,
				"Title":     o.Title,
				"Game":      o.Game,
				"Total":     o.Total,
				"Status":    o.Status,
				"BuyerName": buyerName,
				"Booster":   o.Booster,
				"CreatedAt": o.CreatedAt,
			},
			"Messages": messages,
		},
	})
}

// Просмотр сообщений заказа
func adminViewMessages(c *gin.Context) {
	orderID := c.Param("id")
	user, _ := c.Get("user")

	rows, _ := database.DB.Query(
		"SELECT id, username, text, created_at FROM messages WHERE order_id = $1 ORDER BY created_at ASC",
		orderID,
	)
	if rows != nil {
		defer rows.Close()
	}

	var messages []Message
	if rows != nil {
		for rows.Next() {
			var m Message
			rows.Scan(&m.ID, &m.Username, &m.Text, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "admin-messages",
		"Data": gin.H{
			"OrderID":  orderID,
			"Messages": messages,
		},
	})
}

// Страница добавления товара админом
func adminAddBoostPage(c *gin.Context) {

	user, _ := c.Get("user")

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "admin-add-boost",
	})
}

func adminAddBoost(c *gin.Context) {
	game := c.PostForm("game")
	title := c.PostForm("title")
	desc := c.PostForm("description")
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	userID, _ := strconv.Atoi(c.PostForm("user_id"))

	database.DB.Exec("INSERT INTO boosts (game, title, description, price, booster, user_id) VALUES ($1,$2,$3,$4,'Admin',$5)",
		game, title, desc, price, userID)

	c.Redirect(302, "/admin/boosts")
}

// Экспорт пользователей
func adminExportUsers(c *gin.Context) {
	rows, _ := database.DB.Query("SELECT id, username, email, balance, orders, created_at FROM users ORDER BY id")
	defer rows.Close()

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=users_export.csv")

	writer := bufio.NewWriter(c.Writer)
	defer writer.Flush()

	writer.WriteString("ID,Имя,Email,Баланс,Заказов,Дата регистрации\n")

	for rows.Next() {
		var id, orders int
		var name, email string
		var balance float64
		var createdAt time.Time
		rows.Scan(&id, &name, &email, &balance, &orders, &createdAt)
		fmt.Fprintf(writer, "%d,%s,%s,%.2f,%d,%s\n", id, name, email, balance, orders, createdAt.Format("02.01.2006"))
	}
}

// Расширенная статистика
func adminStatsPage(c *gin.Context) {
	var totalUsers, totalOrders, totalBoosts, completedOrders int
	var totalRevenue, avgOrderValue float64

	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&totalUsers)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&totalOrders)
	database.DB.QueryRow("SELECT COUNT(*) FROM boosts").Scan(&totalBoosts)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE status = 'completed'").Scan(&completedOrders)
	database.DB.QueryRow("SELECT COALESCE(SUM(total), 0) FROM orders WHERE status = 'completed'").Scan(&totalRevenue)
	database.DB.QueryRow("SELECT COALESCE(AVG(total), 0) FROM orders").Scan(&avgOrderValue)

	user, _ := c.Get("user")

	// Заказов сегодня
	var todayOrders int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE DATE(created_at) = CURRENT_DATE").Scan(&todayOrders)

	// Новых пользователей сегодня
	var todayUsers int
	database.DB.QueryRow("SELECT COUNT(*) FROM users WHERE DATE(created_at) = CURRENT_DATE").Scan(&todayUsers)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-stats",
		"User":   user,
		"Data": gin.H{
			"TotalUsers":      totalUsers,
			"TotalOrders":     totalOrders,
			"TotalBoosts":     totalBoosts,
			"CompletedOrders": completedOrders,
			"TotalRevenue":    totalRevenue,
			"AvgOrderValue":   avgOrderValue,
			"TodayOrders":     todayOrders,
			"TodayUsers":      todayUsers,
		},
	})
}

// Страница с графиками
func adminChartsPage(c *gin.Context) {
	// Данные по дням за последние 7 дней
	rows, _ := database.DB.Query(`
        SELECT DATE(created_at) as day, COUNT(*), COALESCE(SUM(total), 0)
        FROM orders 
        WHERE created_at >= NOW() - INTERVAL '7 days'
        GROUP BY DATE(created_at)
        ORDER BY day
    `)
	defer rows.Close()

	user, _ := c.Get("user")

	type DayData struct {
		Date  string
		Count int
		Total float64
	}

	var chartData []DayData
	for rows.Next() {
		var d DayData
		var day time.Time
		rows.Scan(&day, &d.Count, &d.Total)
		d.Date = day.Format("02.01")
		chartData = append(chartData, d)
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-charts",
		"User":   user,
		"Data":   gin.H{"ChartData": chartData},
	})
}

// Транзакции
func adminTransactionsPage(c *gin.Context) {
	// Создаем таблицу если нужно
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS transactions (
        id SERIAL PRIMARY KEY,
        user_id INTEGER,
        type VARCHAR(50),
        amount DECIMAL(10,2),
        description TEXT,
        created_at TIMESTAMP DEFAULT NOW()
    )`)

	rows, _ := database.DB.Query(`
        SELECT t.id, u.username, t.type, t.amount, t.description, t.created_at 
        FROM transactions t 
        LEFT JOIN users u ON t.user_id = u.id 
        ORDER BY t.created_at DESC LIMIT 50
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type Transaction struct {
		ID          int
		Username    string
		Type        string
		Amount      float64
		Description string
		CreatedAt   time.Time
	}

	var transactions []Transaction
	if rows != nil {
		for rows.Next() {
			var t Transaction
			rows.Scan(&t.ID, &t.Username, &t.Type, &t.Amount, &t.Description, &t.CreatedAt)
			transactions = append(transactions, t)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-transactions",
		"User":   user,
		"Data":   gin.H{"Transactions": transactions},
	})
}

// Возврат средств
func adminRefundOrder(c *gin.Context) {
	orderID := c.Param("id")

	var userID int
	var total float64
	database.DB.QueryRow("SELECT user_id, total FROM orders WHERE id = $1", orderID).Scan(&userID, &total)

	// Возвращаем деньги
	database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", total, userID)
	database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)

	// Запись транзакции
	database.DB.Exec("INSERT INTO transactions (user_id, type, amount, description) VALUES ($1, 'refund', $2, $3)",
		userID, total, "Возврат по заказу #"+orderID)

	// Лог админа
	session, _ := store.Get(c.Request, "xsonebmp-session")
	adminID, _ := session.Values["admin_id"]
	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'refund', $2)",
		adminID, "Возврат по заказу #"+orderID)

	c.Redirect(302, "/admin/orders?success=Возврат+оформлен")
}

// Редактирование товара админом
func adminEditBoostPage(c *gin.Context) {

	user, _ := c.Get("user")

	var b Boost
	database.DB.QueryRow("SELECT id, game, title, description, price FROM boosts WHERE id = $1", c.Param("id")).
		Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-edit-boost",
		"User":   user,
		"Data":   gin.H{"Boost": b},
	})
}

func adminEditBoost(c *gin.Context) {
	id := c.Param("id")
	game := c.PostForm("game")
	title := c.PostForm("title")
	desc := c.PostForm("description")
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)

	database.DB.Exec("UPDATE boosts SET game=$1, title=$2, description=$3, price=$4 WHERE id=$5",
		game, title, desc, price, id)

	c.Redirect(302, "/admin/boosts")
}

// Создание пользователя админом
func adminCreateUser(c *gin.Context) {
	username := c.PostForm("username")
	email := c.PostForm("email")
	password := c.PostForm("password")
	balance, _ := strconv.ParseFloat(c.PostForm("balance"), 64)

	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

	database.DB.Exec("INSERT INTO users (username, email, password, balance) VALUES ($1,$2,$3,$4)",
		username, email, string(hash), balance)

	c.Redirect(302, "/admin/users?success=Пользователь+создан")
}

// Логи админа
func adminViewLogs(c *gin.Context) {
	// Убедитесь что таблица создана
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS admin_logs (
        id SERIAL PRIMARY KEY,
        admin_id INTEGER,
        action TEXT,
        details TEXT,
        created_at TIMESTAMP DEFAULT NOW()
    )`)

	rows, _ := database.DB.Query(`
        SELECT a.id, a.admin_id, u.username, a.action, a.details, a.created_at 
        FROM admin_logs a 
        LEFT JOIN users u ON a.admin_id = u.id 
        ORDER BY a.created_at DESC LIMIT 100
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type LogEntry struct {
		ID        int
		AdminID   int
		Username  string
		Action    string
		Details   string
		CreatedAt time.Time
	}

	var logs []LogEntry
	if rows != nil {
		for rows.Next() {
			var l LogEntry
			rows.Scan(&l.ID, &l.AdminID, &l.Username, &l.Action, &l.Details, &l.CreatedAt)
			logs = append(logs, l)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-logs",
		"User":   user,
		"Data":   gin.H{"Logs": logs},
	})
}

// Настройки
func adminUpdateSettings(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil || user.(*User).ID != 1 {
		c.JSON(403, gin.H{"error": "Доступ запрещён"})
		return
	}

	var updates map[string]string
	if err := c.BindJSON(&updates); err != nil {
		c.JSON(400, gin.H{"error": "Неверный JSON"})
		return
	}

	// Валидация
	for key, newValue := range updates {
		switch key {
		case "platform_fee":
			fee, err := strconv.ParseFloat(newValue, 64)
			if err != nil || fee < 0 || fee > 50 {
				c.JSON(400, gin.H{"error": "Комиссия должна быть от 0 до 50%"})
				return
			}
		case "min_withdraw":
			min, err := strconv.ParseFloat(newValue, 64)
			if err != nil || min < 10 {
				c.JSON(400, gin.H{"error": "Минимальный вывод от 10"})
				return
			}
		case "referral_percent", "referral_level2":
			val, err := strconv.ParseFloat(newValue, 64)
			if err != nil || val < 0 || val > 50 {
				c.JSON(400, gin.H{"error": "Процент рефералки от 0 до 50"})
				return
			}
		}
	}

	// Обновление с логированием
	for key, newValue := range updates {
		var oldValue string
		database.DB.QueryRow("SELECT value FROM settings WHERE key = $1", key).Scan(&oldValue)
		if oldValue != newValue {
			database.DB.Exec(`INSERT INTO settings_history (setting_key, old_value, new_value, changed_by, changed_at) 
                VALUES ($1, $2, $3, $4, NOW())`, key, oldValue, newValue, user.(*User).ID)
		}
		database.DB.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = $2`, key, newValue)
	}

	// Обновляем кэш (если используете)
	go loadSettingsToCache()

	c.JSON(200, gin.H{"success": true})
}

// Верификация продавца
func adminVerifySeller(c *gin.Context) {
	userID := c.Param("id")
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS verified_sellers (
        user_id INTEGER PRIMARY KEY,
        verified_at TIMESTAMP DEFAULT NOW()
    )`)
	database.DB.Exec("INSERT INTO verified_sellers (user_id) VALUES ($1) ON CONFLICT DO NOTHING", userID)
	c.Redirect(302, "/admin/users?success=Продавец+верифицирован")
}

// Избранный товар
func adminFeatureBoost(c *gin.Context) {
	boostID := c.Param("id")
	database.DB.Exec(`CREATE TABLE IF NOT EXISTS featured_boosts (
        boost_id INTEGER PRIMARY KEY,
        featured_at TIMESTAMP DEFAULT NOW()
    )`)
	database.DB.Exec("INSERT INTO featured_boosts (boost_id) VALUES ($1) ON CONFLICT DO NOTHING", boostID)
	c.Redirect(302, "/admin/boosts?success=Товар+добавлен+в+избранное")
}

// Получить уровень продавца
func getSellerLevel(orders int, rating float64) (string, string, int) {
	var levelName, color string
	var level int

	// Если рейтинг 0 - считаем как 5.0 (начальный)
	if rating == 0 {
		rating = 5.0
	}

	rows, _ := database.DB.Query(
		"SELECT level, name, color FROM seller_levels WHERE min_orders <= $1 AND min_rating <= $2 ORDER BY level DESC LIMIT 1",
		orders, rating,
	)
	if rows != nil {
		defer rows.Close()
		if rows.Next() {
			rows.Scan(&level, &levelName, &color)
		}
	}

	if level == 0 {
		level = 1
		levelName = "Новичок"
		color = "#10b981"
	}

	return levelName, color, level
}

// Проверить достижения
func checkAchievements(userID int) {
	// Считаем ПРОДАЖИ (где пользователь - booster)
	var salesCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE booster_id = $1 AND status = 'completed'", userID).Scan(&salesCount)

	// Рейтинг продавца
	var rating float64
	database.DB.QueryRow("SELECT COALESCE(rating, 0) FROM users WHERE id = $1", userID).Scan(&rating)

	// Заработок с продаж
	var earned float64
	database.DB.QueryRow("SELECT COALESCE(SUM(total), 0) FROM orders WHERE booster_id = $1 AND status = 'completed'", userID).Scan(&earned)

	rows, _ := database.DB.Query("SELECT id, condition_field, condition_value FROM achievements")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var id int
			var field string
			var value int
			rows.Scan(&id, &field, &value)

			var achieved bool
			switch field {
			case "orders":
				achieved = salesCount >= value // ← ИСПРАВЛЕНО: считаем продажи
			case "rating":
				achieved = int(rating*10) >= value // 4.5 → 45
			case "earned":
				achieved = int(earned) >= value
			}

			if achieved {
				database.DB.Exec("INSERT INTO user_achievements (user_id, achievement_id) VALUES ($1,$2) ON CONFLICT DO NOTHING", userID, id)
			}
		}
	}
}

// Страница достижений в профиле
func achievementsPage(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists || u == nil {
		c.Redirect(302, "/login")
		return
	}

	user, ok := u.(*User)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Обновляем достижения на основе ПРОДАЖ
	checkAchievements(user.ID)

	// Считаем продажи
	var salesCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE booster_id = $1 AND status = 'completed'", user.ID).Scan(&salesCount)

	// Уровень продавца
	levelName, levelColor, _ := getSellerLevel(salesCount, user.Rating)

	// Все достижения
	rows, _ := database.DB.Query(`
        SELECT a.id, a.name, a.description, a.icon, 
               CASE WHEN ua.user_id IS NOT NULL THEN true ELSE false END as achieved
        FROM achievements a 
        LEFT JOIN user_achievements ua ON a.id = ua.achievement_id AND ua.user_id = $1
        ORDER BY a.id
    `, user.ID)
	if rows != nil {
		defer rows.Close()
	}

	type Achievement struct {
		ID          int
		Name        string
		Description string
		Icon        string
		Achieved    bool
	}

	var achievements []Achievement
	if rows != nil {
		for rows.Next() {
			var a Achievement
			rows.Scan(&a.ID, &a.Name, &a.Description, &a.Icon, &a.Achieved)
			achievements = append(achievements, a)
		}
	}

	// Следующий уровень
	var nextLevelName string
	var ordersNeeded int
	database.DB.QueryRow("SELECT name, min_orders FROM seller_levels WHERE min_orders > $1 ORDER BY level ASC LIMIT 1", salesCount).
		Scan(&nextLevelName, &ordersNeeded)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   user,
		"Active": "achievements",
		"Data": gin.H{
			"LevelName":    levelName,
			"LevelColor":   levelColor,
			"Achievements": achievements,
			"NextLevel":    nextLevelName,
			"OrdersNeeded": ordersNeeded - salesCount,
		},
	})
}

func checkPromoCode(c *gin.Context) {
	var request struct {
		Code   string  `json:"code"`
		Amount float64 `json:"amount"`
	}
	json.NewDecoder(c.Request.Body).Decode(&request)

	if request.Code == "" {
		c.JSON(400, gin.H{"success": false, "message": "Введите промокод"})
		return
	}

	var promo struct {
		ID              int
		DiscountPercent int
		MaxUses         int
		UsedCount       int
		MinOrderAmount  float64
		IsActive        bool
	}

	err := database.DB.QueryRow(
		"SELECT id, discount_percent, max_uses, used_count, min_order_amount, is_active FROM promocodes WHERE code = $1",
		request.Code,
	).Scan(&promo.ID, &promo.DiscountPercent, &promo.MaxUses, &promo.UsedCount, &promo.MinOrderAmount, &promo.IsActive)

	if err != nil {
		c.JSON(404, gin.H{"success": false, "message": "Промокод не найден"})
		return
	}

	if !promo.IsActive {
		c.JSON(400, gin.H{"success": false, "message": "Промокод недействителен"})
		return
	}

	if promo.MaxUses > 0 && promo.UsedCount >= promo.MaxUses {
		c.JSON(400, gin.H{"success": false, "message": "Лимит использования исчерпан"})
		return
	}

	if request.Amount < promo.MinOrderAmount {
		c.JSON(400, gin.H{"success": false, "message": fmt.Sprintf("Минимальная сумма заказа: %.0f ₽", promo.MinOrderAmount)})
		return
	}

	discountAmount := request.Amount * float64(promo.DiscountPercent) / 100

	c.JSON(200, gin.H{
		"success":          true,
		"message":          fmt.Sprintf("Промокод применен! Скидка %d%%", promo.DiscountPercent),
		"discount_percent": promo.DiscountPercent,
		"discount_amount":  discountAmount,
		"final_amount":     request.Amount - discountAmount,
	})
}

// Страница промокодов в админке
func adminPromocodesPage(c *gin.Context) {
	rows, _ := database.DB.Query("SELECT id, code, discount_percent, max_uses, used_count, is_active, created_at FROM promocodes ORDER BY id DESC")
	if rows != nil {
		defer rows.Close()
	}
	user, _ := c.Get("user")
	type Promo struct {
		ID              int
		Code            string
		DiscountPercent int
		MaxUses         int
		UsedCount       int
		IsActive        bool
		CreatedAt       time.Time
	}

	var promos []Promo
	if rows != nil {
		for rows.Next() {
			var p Promo
			rows.Scan(&p.ID, &p.Code, &p.DiscountPercent, &p.MaxUses, &p.UsedCount, &p.IsActive, &p.CreatedAt)
			promos = append(promos, p)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-promocodes",
		"User":   user,
		"Data":   gin.H{"Promos": promos},
	})
}

func adminCreatePromocode(c *gin.Context) {
	code := c.PostForm("code")
	discount, _ := strconv.Atoi(c.PostForm("discount"))
	maxUses, _ := strconv.Atoi(c.PostForm("max_uses"))

	database.DB.Exec("INSERT INTO promocodes (code, discount_percent, max_uses) VALUES ($1,$2,$3)",
		code, discount, maxUses)

	c.Redirect(302, "/admin/promocodes")
}

func adminDeletePromocode(c *gin.Context) {
	database.DB.Exec("DELETE FROM promocodes WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/promocodes")
}

func adminTogglePromocode(c *gin.Context) {
	database.DB.Exec("UPDATE promocodes SET is_active = NOT is_active WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/promocodes")
}

func checkFirstOrderDiscount(userID int) (bool, int) {
	var orderCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE user_id = $1", userID).Scan(&orderCount)

	if orderCount == 0 {
		var discountPercent int
		var isActive bool
		database.DB.QueryRow("SELECT discount_percent, is_active FROM first_order_discount LIMIT 1").Scan(&discountPercent, &isActive)

		if isActive {
			return true, discountPercent
		}
	}

	return false, 0
}

// Проверка сезонных акций
func checkSeasonalSale() (bool, string, int) {
	var sale struct {
		Name            string
		DiscountPercent int
	}

	err := database.DB.QueryRow(
		"SELECT name, discount_percent FROM sales WHERE is_active = true AND NOW() BETWEEN start_date AND end_date ORDER BY discount_percent DESC LIMIT 1",
	).Scan(&sale.Name, &sale.DiscountPercent)

	if err == nil {
		return true, sale.Name, sale.DiscountPercent
	}

	return false, "", 0
}

// API для получения доступных скидок
func getAvailableDiscounts(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	var discounts []gin.H

	// Скидка на первый заказ
	if userID != nil {
		hasFirstDiscount, firstDiscount := checkFirstOrderDiscount(userID.(int))
		if hasFirstDiscount {
			discounts = append(discounts, gin.H{
				"type":    "first_order",
				"name":    "Скидка на первый заказ",
				"percent": firstDiscount,
				"code":    "FIRST",
			})
		}
	}

	// Сезонная акция
	hasSale, saleName, saleDiscount := checkSeasonalSale()
	if hasSale {
		discounts = append(discounts, gin.H{
			"type":    "seasonal",
			"name":    saleName,
			"percent": saleDiscount,
			"code":    "SEASON",
		})
	}

	c.JSON(200, gin.H{"discounts": discounts})
}

func applyDiscount(c *gin.Context) {
	var request struct {
		Code   string  `json:"code"`
		Amount float64 `json:"amount"`
	}
	json.NewDecoder(c.Request.Body).Decode(&request)

	switch request.Code {
	case "FIRST":
		session, _ := store.Get(c.Request, "xsonebmp-session")
		userID, _ := session.Values["user_id"]

		hasDiscount, percent := checkFirstOrderDiscount(userID.(int))
		if hasDiscount {
			discountAmount := request.Amount * float64(percent) / 100
			c.JSON(200, gin.H{
				"success":          true,
				"message":          fmt.Sprintf("Скидка на первый заказ %d%% применена!", percent),
				"discount_percent": percent,
				"discount_amount":  discountAmount,
				"final_amount":     request.Amount - discountAmount,
			})
			return
		}

	case "SEASON":
		hasSale, saleName, percent := checkSeasonalSale()
		if hasSale {
			discountAmount := request.Amount * float64(percent) / 100
			c.JSON(200, gin.H{
				"success":          true,
				"message":          fmt.Sprintf("%s: скидка %d%% применена!", saleName, percent),
				"discount_percent": percent,
				"discount_amount":  discountAmount,
				"final_amount":     request.Amount - discountAmount,
			})
			return
		}
	}

	c.JSON(400, gin.H{"success": false, "message": "Скидка недоступна"})
}

func adminSalesPage(c *gin.Context) {
	// Активные акции
	salesRows, _ := database.DB.Query("SELECT id, name, discount_percent, start_date, end_date, is_active FROM sales ORDER BY id DESC")
	if salesRows != nil {
		defer salesRows.Close()
	}

	user, _ := c.Get("user")

	type Sale struct {
		ID        int
		Name      string
		Discount  int
		StartDate time.Time
		EndDate   time.Time
		IsActive  bool
	}

	var sales []Sale
	if salesRows != nil {
		for salesRows.Next() {
			var s Sale
			salesRows.Scan(&s.ID, &s.Name, &s.Discount, &s.StartDate, &s.EndDate, &s.IsActive)
			sales = append(sales, s)
		}
	}

	// Скидка на первый заказ
	var firstDiscount int
	var firstActive bool
	database.DB.QueryRow("SELECT discount_percent, is_active FROM first_order_discount LIMIT 1").Scan(&firstDiscount, &firstActive)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-sales",
		"User":   user,
		"Data": gin.H{
			"Sales":         sales,
			"FirstDiscount": firstDiscount,
			"FirstActive":   firstActive,
		},
	})
}

func adminCreateSale(c *gin.Context) {
	name := c.PostForm("name")
	discount, _ := strconv.Atoi(c.PostForm("discount"))
	startDate := c.PostForm("start_date")
	endDate := c.PostForm("end_date")

	database.DB.Exec("INSERT INTO sales (name, discount_percent, start_date, end_date) VALUES ($1,$2,$3,$4)",
		name, discount, startDate, endDate)

	c.Redirect(302, "/admin/sales")
}

func adminToggleSale(c *gin.Context) {
	database.DB.Exec("UPDATE sales SET is_active = NOT is_active WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/sales")
}

func adminDeleteSale(c *gin.Context) {
	database.DB.Exec("DELETE FROM sales WHERE id = $1", c.Param("id"))
	c.Redirect(302, "/admin/sales")
}

func adminUpdateFirstDiscount(c *gin.Context) {
	discount, _ := strconv.Atoi(c.PostForm("discount"))
	isActive := c.PostForm("is_active") == "on"

	database.DB.Exec("UPDATE first_order_discount SET discount_percent = $1, is_active = $2", discount, isActive)
	c.Redirect(302, "/admin/sales")
}

// Страница открытия диспута
func openDisputePage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	orderID := c.Param("id")

	// Проверяем что пользователь - покупатель ИЛИ продавец
	var isBuyer, isSeller bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND user_id = $2)", orderID, userID).Scan(&isBuyer)
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND booster_id = $2)", orderID, userID).Scan(&isSeller)

	if !isBuyer && !isSeller {
		c.String(404, "Заказ не найден")
		return
	}

	// Проверяем что заказ существует
	var o Order
	err := database.DB.QueryRow(
		"SELECT id, title, total, status FROM orders WHERE id = $1", orderID,
	).Scan(&o.ID, &o.Title, &o.Total, &o.Status)

	if err != nil {
		c.Redirect(302, "/profile?error=Заказ+не+найден")
		return
	}

	// Проверяем что диспут еще не открыт
	var disputeID int
	database.DB.QueryRow("SELECT COALESCE(id, 0) FROM disputes WHERE order_id = $1", orderID).Scan(&disputeID)
	if disputeID > 0 {
		c.Redirect(302, "/dispute/"+fmt.Sprintf("%d", disputeID))
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "dispute-open",
		"Data":   gin.H{"Order": o},
	})
}

// Создание диспута
func createDispute(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("id")
	reason := c.PostForm("reason")
	description := c.PostForm("description")

	var disputeID int
	err := database.DB.QueryRow(
		"INSERT INTO disputes (order_id, user_id, reason, description) VALUES ($1,$2,$3,$4) RETURNING id",
		orderID, userID, reason, description,
	).Scan(&disputeID)

	if err != nil {
		c.Redirect(302, "/profile?error=Ошибка+создания+диспута")
		return
	}

	// Обновляем статус заказа
	database.DB.Exec("UPDATE orders SET status = 'disputed' WHERE id = $1", orderID)

	// Уведомление продавцу
	var boosterID int
	database.DB.QueryRow("SELECT booster_id FROM orders WHERE id = $1", orderID).Scan(&boosterID)

	notifText := fmt.Sprintf("⚠️ Открыт диспут по заказу #%s", orderID)
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		boosterID, notifText, "/dispute/"+fmt.Sprintf("%d", disputeID))
	go sendPushNotification(boosterID, "Открыт диспут", notifText, "/dispute/"+fmt.Sprintf("%d", disputeID))

	c.Redirect(302, "/dispute/"+fmt.Sprintf("%d", disputeID))
}

// Страница диспута
func disputeDetailPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	disputeID := c.Param("id")

	var dispute struct {
		ID            int
		OrderID       int
		UserID        int
		Reason        string
		Description   string
		Status        string
		AdminDecision string
		CreatedAt     time.Time
	}

	err := database.DB.QueryRow(
		"SELECT id, order_id, user_id, reason, description, status, COALESCE(admin_decision, ''), created_at FROM disputes WHERE id = $1",
		disputeID,
	).Scan(&dispute.ID, &dispute.OrderID, &dispute.UserID, &dispute.Reason, &dispute.Description, &dispute.Status, &dispute.AdminDecision, &dispute.CreatedAt)

	if err != nil {
		c.String(404, "Спор не найден")
		return
	}

	// Проверяем доступ: админ (ID=1), покупатель ИЛИ продавец
	isAdminUser := userID == 1
	isBuyer := userID == dispute.UserID

	var isSeller bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND booster_id = $2)", dispute.OrderID, userID).Scan(&isSeller)

	if !isAdminUser && !isBuyer && !isSeller {
		c.Redirect(302, "/profile")
		return
	}

	// Если админ зашел - оставляем системное сообщение
	if isAdminUser {
		var adminMsgCount int
		database.DB.QueryRow(
			"SELECT COUNT(*) FROM dispute_messages WHERE dispute_id = $1 AND is_admin = true AND message LIKE '👑%'",
			disputeID,
		).Scan(&adminMsgCount)

		if adminMsgCount == 0 {
			var adminUsername string
			database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&adminUsername)
			database.DB.Exec(
				"INSERT INTO dispute_messages (dispute_id, user_id, username, message, is_admin) VALUES ($1, $2, $3, '👑 Администратор подключился к спору и начал рассмотрение.', true)",
				disputeID, userID, adminUsername,
			)
		}
	}

	// Сообщения диспута
	msgRows, _ := database.DB.Query(
		"SELECT id, user_id, username, message, is_admin, created_at FROM dispute_messages WHERE dispute_id = $1 ORDER BY created_at ASC",
		disputeID,
	)
	if msgRows != nil {
		defer msgRows.Close()
	}

	type DisputeMessage struct {
		ID        int
		UserID    int
		Username  string
		Message   string
		IsAdmin   bool
		CreatedAt time.Time
	}

	var messages []DisputeMessage
	if msgRows != nil {
		for msgRows.Next() {
			var m DisputeMessage
			msgRows.Scan(&m.ID, &m.UserID, &m.Username, &m.Message, &m.IsAdmin, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	// Информация о заказе
	var order struct {
		ID      int
		Title   string
		Total   float64
		Status  string
		Booster string
	}
	database.DB.QueryRow(
		"SELECT o.id, o.title, o.total, o.status, u.username FROM orders o JOIN users u ON o.booster_id = u.id WHERE o.id = $1",
		dispute.OrderID,
	).Scan(&order.ID, &order.Title, &order.Total, &order.Status, &order.Booster)

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "dispute-detail",
		"Data": gin.H{
			"Dispute":  dispute,
			"Messages": messages,
			"Order":    order,
			"IsAdmin":  isAdminUser,
		},
	})
}

// Отправка сообщения в диспуте
func disputeSendMessage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	// Проверяем админ ли это
	isAdminUser := userID == 1

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	disputeID := c.Param("id")
	message := c.PostForm("message")

	if message != "" {
		database.DB.Exec(
			"INSERT INTO dispute_messages (dispute_id, user_id, username, message, is_admin) VALUES ($1,$2,$3,$4,$5)",
			disputeID, userID, username, message, isAdminUser,
		)
	}

	c.Redirect(302, "/dispute/"+disputeID)
}

// Решение админа по диспуту
func adminResolveDispute(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Только админ (ID=1) может решать споры
	if userID != 1 {
		c.Redirect(302, "/")
		return
	}

	disputeID := c.Param("id")
	decision := c.PostForm("decision")
	comment := c.PostForm("comment")

	// Получаем реальное имя админа
	var adminUsername string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&adminUsername)

	// Получаем данные спора
	var orderID, buyerID, boosterID int
	var total float64
	err := database.DB.QueryRow("SELECT order_id, user_id FROM disputes WHERE id = $1", disputeID).Scan(&orderID, &buyerID)
	if err != nil {
		c.Redirect(302, "/admin/disputes")
		return
	}
	database.DB.QueryRow("SELECT booster_id, total FROM orders WHERE id = $1", orderID).Scan(&boosterID, &total)

	// Обновляем диспут
	database.DB.Exec(
		"UPDATE disputes SET status = 'resolved', admin_decision = $1, resolved_at = NOW() WHERE id = $2",
		comment, disputeID,
	)

	// Сообщение о решении
	var decisionText string

	if decision == "refund" {
		// Возврат денег покупателю
		decisionText = "💰 Решение: возврат денег покупателю. " + comment

		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", total, buyerID)
		database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)

		// История транзакций
		var buyerBalance float64
		database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", buyerID).Scan(&buyerBalance)
		database.DB.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'refund',$2,$3,$4,'Возврат по спору #'+$5,$6)",
			buyerID, total, buyerBalance-total, buyerBalance, disputeID, orderID)

		// Уведомления
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			buyerID, fmt.Sprintf("✅ Возврат %.0f ₽ по спору #%s", total, disputeID), "/dispute/"+disputeID)
		go sendPushNotification(buyerID, "Спор решён", fmt.Sprintf("✅ Возврат %.0f ₽", total), "/dispute/"+disputeID)
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			boosterID, fmt.Sprintf("❌ Спор #%s решен в пользу покупателя", disputeID), "/dispute/"+disputeID)
		go sendPushNotification(boosterID, "Спор решён", fmt.Sprintf("❌ В пользу покупателя"), "/dispute/"+disputeID)

	} else if decision == "complete" {
		// Заказ в пользу продавца
		decisionText = "✅ Решение: заказ выполнен, деньги переведены продавцу. " + comment

		// Выплата продавцу (с комиссией 5%)
		fee := total * 0.05
		sellerAmount := total - fee

		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", sellerAmount, boosterID)
		database.DB.Exec("UPDATE orders SET status = 'completed' WHERE id = $1", orderID)
		database.DB.Exec("INSERT INTO platform_fees (order_id, amount, fee_percent) VALUES ($1,$2,5.00)", orderID, fee)

		// История транзакций продавца
		var sellerBalance float64
		database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", boosterID).Scan(&sellerBalance)
		database.DB.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'income',$2,$3,$4,'Выплата по спору #'+$5,$6)",
			boosterID, sellerAmount, sellerBalance-sellerAmount, sellerBalance, disputeID, orderID)

		// Уведомления
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			boosterID, fmt.Sprintf("✅ Спор #%s решен в вашу пользу! +%.0f ₽", disputeID, sellerAmount), "/dispute/"+disputeID)
		go sendPushNotification(boosterID, "Спор решён", fmt.Sprintf("✅ В вашу пользу! +%.0f ₽", sellerAmount), "/dispute/"+disputeID)
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			buyerID, fmt.Sprintf("❌ Спор #%s решен в пользу продавца", disputeID), "/dispute/"+disputeID)
		go sendPushNotification(buyerID, "Спор решён", "❌ В пользу продавца", "/dispute/"+disputeID)
	}

	// Сообщение админа в чат спора с реальным именем
	database.DB.Exec(
		"INSERT INTO dispute_messages (dispute_id, user_id, username, message, is_admin) VALUES ($1, $2, $3, $4, true)",
		disputeID, userID, adminUsername, decisionText,
	)

	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'resolve_dispute', 'Решение по спору #'+$2)", userID, disputeID)

	c.Redirect(302, "/dispute/"+disputeID)
}

// Админка - все диспуты
func adminDisputesPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT d.id, d.order_id, u.username, d.reason, d.status, d.created_at 
        FROM disputes d 
        JOIN users u ON d.user_id = u.id 
        ORDER BY d.created_at DESC
    `)
	if rows != nil {
		defer rows.Close()
	}
	user, _ := c.Get("user")

	type DisputeRow struct {
		ID        int
		OrderID   int
		Username  string
		Reason    string
		Status    string
		CreatedAt time.Time
	}

	var disputes []DisputeRow
	if rows != nil {
		for rows.Next() {
			var d DisputeRow
			rows.Scan(&d.ID, &d.OrderID, &d.Username, &d.Reason, &d.Status, &d.CreatedAt)
			disputes = append(disputes, d)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-disputes",
		"User":   user,
		"Data":   gin.H{"Disputes": disputes},
	})
}

func getReviewLikes(c *gin.Context) {
	reviewID := c.Param("id")

	var count int
	database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1", reviewID).Scan(&count)

	// Проверяем лайкнул ли текущий пользователь
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	var liked bool
	if userID != nil {
		var exists int
		database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1 AND user_id = $2", reviewID, userID).Scan(&exists)
		liked = exists > 0
	}

	c.JSON(200, gin.H{"count": count, "liked": liked})
}

func toggleReviewLike(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}

	reviewID := c.Param("id")
	userIDint := userID.(int)

	// Проверяем есть ли лайк
	var exists int
	database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1 AND user_id = $2", reviewID, userIDint).Scan(&exists)

	if exists > 0 {
		// Убираем лайк
		database.DB.Exec("DELETE FROM review_likes WHERE review_id = $1 AND user_id = $2", reviewID, userIDint)
	} else {
		// Ставим лайк
		database.DB.Exec("INSERT INTO review_likes (review_id, user_id) VALUES ($1,$2)", reviewID, userIDint)
	}

	var count int
	database.DB.QueryRow("SELECT COUNT(*) FROM review_likes WHERE review_id = $1", reviewID).Scan(&count)

	c.JSON(200, gin.H{"count": count, "liked": exists == 0})
}

// Страница тикетов пользователя
func supportPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	rows, _ := database.DB.Query(
		"SELECT id, category, subject, status, priority, created_at, updated_at FROM tickets WHERE user_id = $1 ORDER BY updated_at DESC",
		userID,
	)
	if rows != nil {
		defer rows.Close()
	}

	type Ticket struct {
		ID        int
		Category  string
		Subject   string
		Status    string
		Priority  string
		CreatedAt time.Time
		UpdatedAt time.Time
	}

	var tickets []Ticket
	if rows != nil {
		for rows.Next() {
			var t Ticket
			rows.Scan(&t.ID, &t.Category, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt)
			tickets = append(tickets, t)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "support",
		"Data":   gin.H{"Tickets": tickets},
	})
}

// Создание тикета
func createTicketPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "support-create",
	})
}

func createTicket(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	category := c.PostForm("category")
	subject := c.PostForm("subject")
	description := c.PostForm("description")
	priority := c.PostForm("priority")

	var ticketID int
	database.DB.QueryRow(
		"INSERT INTO tickets (user_id, category, subject, description, priority) VALUES ($1,$2,$3,$4,$5) RETURNING id",
		userID, category, subject, description, priority,
	).Scan(&ticketID)

	c.Redirect(302, "/support/ticket/"+fmt.Sprintf("%d", ticketID))
}

// Детали тикета
func ticketDetailPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	ticketID := c.Param("id")

	var ticket struct {
		ID          int
		UserID      int
		Category    string
		Subject     string
		Description string
		Status      string
		Priority    string
		CreatedAt   time.Time
	}

	database.DB.QueryRow(
		"SELECT id, user_id, category, subject, description, status, priority, created_at FROM tickets WHERE id = $1 AND user_id = $2",
		ticketID, userID,
	).Scan(&ticket.ID, &ticket.UserID, &ticket.Category, &ticket.Subject, &ticket.Description, &ticket.Status, &ticket.Priority, &ticket.CreatedAt)

	// Сообщения
	msgRows, _ := database.DB.Query(
		"SELECT id, username, message, is_admin, created_at FROM ticket_messages WHERE ticket_id = $1 ORDER BY created_at ASC",
		ticketID,
	)
	if msgRows != nil {
		defer msgRows.Close()
	}

	type TicketMessage struct {
		ID        int
		Username  string
		Message   string
		IsAdmin   bool
		CreatedAt time.Time
	}

	var messages []TicketMessage
	if msgRows != nil {
		for msgRows.Next() {
			var m TicketMessage
			msgRows.Scan(&m.ID, &m.Username, &m.Message, &m.IsAdmin, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "support-ticket",
		"Data":   gin.H{"Ticket": ticket, "Messages": messages},
	})
}

// Отправка сообщения в тикет
func ticketSendMessage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	ticketID := c.Param("id")
	message := c.PostForm("message")

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	if message != "" {
		database.DB.Exec(
			"INSERT INTO ticket_messages (ticket_id, user_id, username, message) VALUES ($1,$2,$3,$4)",
			ticketID, userID, username, message,
		)
		database.DB.Exec("UPDATE tickets SET updated_at = NOW() WHERE id = $1", ticketID)
	}

	c.Redirect(302, "/support/ticket/"+ticketID)
}

// Закрытие тикета
func closeTicket(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	ticketID := c.Param("id")
	database.DB.Exec("UPDATE tickets SET status = 'closed', updated_at = NOW() WHERE id = $1 AND user_id = $2", ticketID, userID)

	c.Redirect(302, "/support/ticket/"+ticketID)
}

// ============ АДМИНКА ТИКЕТЫ ============

func adminTicketsPage(c *gin.Context) {
	filter := c.Query("status")

	query := "SELECT id, user_id, category, subject, status, priority, created_at FROM tickets"

	if filter == "open" {
		query += " WHERE status = 'open'"
	} else if filter == "in_progress" {
		query += " WHERE status = 'in_progress'"
	} else if filter == "closed" {
		query += " WHERE status = 'closed'"
	}

	query += " ORDER BY CASE status WHEN 'open' THEN 1 WHEN 'in_progress' THEN 2 WHEN 'closed' THEN 3 END, CASE priority WHEN 'urgent' THEN 1 WHEN 'high' THEN 2 WHEN 'normal' THEN 3 WHEN 'low' THEN 4 END, created_at DESC"

	rows, _ := database.DB.Query(query)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type TicketRow struct {
		ID        int
		UserID    int
		Category  string
		Subject   string
		Status    string
		Priority  string
		CreatedAt time.Time
	}

	var tickets []TicketRow
	if rows != nil {
		for rows.Next() {
			var t TicketRow
			rows.Scan(&t.ID, &t.UserID, &t.Category, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt)
			tickets = append(tickets, t)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-tickets",
		"User":   user,
		"Data":   gin.H{"Tickets": tickets, "Filter": filter},
	})
}

func adminTicketDetail(c *gin.Context) {
	ticketID := c.Param("id")
	user, _ := c.Get("user")
	var ticket struct {
		ID          int
		UserID      int
		Category    string
		Subject     string
		Description string
		Status      string
		Priority    string
		CreatedAt   time.Time
	}

	database.DB.QueryRow(
		"SELECT t.id, t.user_id, t.category, t.subject, t.description, t.status, t.priority, t.created_at FROM tickets t WHERE t.id = $1",
		ticketID,
	).Scan(&ticket.ID, &ticket.UserID, &ticket.Category, &ticket.Subject, &ticket.Description, &ticket.Status, &ticket.Priority, &ticket.CreatedAt)

	// Сообщения
	msgRows, _ := database.DB.Query(
		"SELECT id, username, message, is_admin, created_at FROM ticket_messages WHERE ticket_id = $1 ORDER BY created_at ASC",
		ticketID,
	)
	if msgRows != nil {
		defer msgRows.Close()
	}

	var messages []gin.H
	if msgRows != nil {
		for msgRows.Next() {
			var id int
			var username, message string
			var isAdmin bool
			var createdAt time.Time
			msgRows.Scan(&id, &username, &message, &isAdmin, &createdAt)
			messages = append(messages, gin.H{"id": id, "username": username, "message": message, "is_admin": isAdmin, "created_at": createdAt})
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-ticket-detail",
		"User":   user,
		"Data":   gin.H{"Ticket": ticket, "Messages": messages},
	})
}

func adminTicketMessage(c *gin.Context) {
	ticketID := c.Param("id")
	message := c.PostForm("message")

	if message != "" {
		database.DB.Exec(
			"INSERT INTO ticket_messages (ticket_id, user_id, username, message, is_admin) VALUES ($1, 0, 'Поддержка XSoneBMP', $2, true)",
			ticketID, message,
		)
		database.DB.Exec("UPDATE tickets SET status = 'in_progress', updated_at = NOW() WHERE id = $1 AND status = 'open'", ticketID)
	}

	c.Redirect(302, "/admin/ticket/"+ticketID)
}

func adminTicketStatus(c *gin.Context) {
	ticketID := c.Param("id")
	status := c.PostForm("status")

	database.DB.Exec("UPDATE tickets SET status = $1, updated_at = NOW() WHERE id = $2", status, ticketID)
	c.Redirect(302, "/admin/ticket/"+ticketID)
}

// Страница эскроу
func escrowPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Покупки (где пользователь - покупатель)
	buyRows, _ := database.DB.Query(`
        SELECT e.id, e.order_id, e.amount, e.status, e.frozen_at, o.title, u.username as seller_name
        FROM escrow_transactions e
        JOIN orders o ON e.order_id = o.id
        JOIN users u ON e.seller_id = u.id
        WHERE e.buyer_id = $1
        ORDER BY e.frozen_at DESC
    `, userID)
	if buyRows != nil {
		defer buyRows.Close()
	}

	// Продажи (где пользователь - продавец)
	sellRows, _ := database.DB.Query(`
        SELECT e.id, e.order_id, e.amount, e.status, e.frozen_at, o.title, u.username as buyer_name
        FROM escrow_transactions e
        JOIN orders o ON e.order_id = o.id
        JOIN users u ON e.buyer_id = u.id
        WHERE e.seller_id = $1
        ORDER BY e.frozen_at DESC
    `, userID)
	if sellRows != nil {
		defer sellRows.Close()
	}

	type EscrowItem struct {
		ID       int
		OrderID  int
		Amount   float64
		Status   string
		FrozenAt time.Time
		Title    string
		Name     string
	}

	var purchases []EscrowItem
	if buyRows != nil {
		for buyRows.Next() {
			var e EscrowItem
			buyRows.Scan(&e.ID, &e.OrderID, &e.Amount, &e.Status, &e.FrozenAt, &e.Title, &e.Name)
			purchases = append(purchases, e)
		}
	}

	var sales []EscrowItem
	if sellRows != nil {
		for sellRows.Next() {
			var e EscrowItem
			sellRows.Scan(&e.ID, &e.OrderID, &e.Amount, &e.Status, &e.FrozenAt, &e.Title, &e.Name)
			sales = append(sales, e)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username, balance FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username, &user.Balance)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "escrow",
		"Data":   gin.H{"Purchases": purchases, "Sales": sales},
	})
}

// Статус эскроу
func escrowStatus(c *gin.Context) {
	orderID := c.Param("orderId")

	var status string
	var amount float64
	database.DB.QueryRow("SELECT status, amount FROM escrow_transactions WHERE order_id = $1", orderID).Scan(&status, &amount)

	c.JSON(200, gin.H{"status": status, "amount": amount})
}

// Выплата продавцу
func releaseEscrow(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("orderId")

	var buyerID, sellerID int
	var amount float64
	var status string
	err := database.DB.QueryRow(
		"SELECT buyer_id, seller_id, amount, status FROM escrow_transactions WHERE order_id = $1", orderID,
	).Scan(&buyerID, &sellerID, &amount, &status)

	if err != nil {
		c.JSON(404, gin.H{"success": false, "message": "Эскроу-транзакция не найдена"})
		return
	}

	if status != "frozen" && status != "ready" {
		c.JSON(400, gin.H{"success": false, "message": "Средства уже не заморожены"})
		return
	}

	if buyerID != userID {
		c.JSON(403, gin.H{"success": false, "message": "Только покупатель может подтвердить"})
		return
	}

	// Комиссия
	var feePercent float64 = 5.0
	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", sellerID).Scan(&isPro)
	if isPro {
		feePercent = 2.0
	}
	fee := amount * feePercent / 100
	sellerAmount := amount - fee

	// Обновляем эскроу
	database.DB.Exec("UPDATE escrow_transactions SET status = 'released', released_at = NOW() WHERE order_id = $1", orderID)

	// Начисляем продавцу
	database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", sellerAmount, sellerID)

	// Комиссия
	database.DB.Exec("INSERT INTO platform_fees (order_id, amount, fee_percent) VALUES ($1,$2,5.00)", orderID, fee)

	// Обновляем заказ
	database.DB.Exec("UPDATE orders SET status = 'completed' WHERE id = $1", orderID)

	// Сообщение в чат
	database.DB.Exec("INSERT INTO messages (order_id, user_id, username, text) VALUES ($1,$2,'Система','✅ Покупатель подтвердил выполнение заказа. Средства переведены продавцу.')",
		orderID, buyerID)

	// История транзакций продавца
	var sellerBalance float64
	database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", sellerID).Scan(&sellerBalance)
	database.DB.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'income',$2,$3,$4,'Выплата по заказу',$5)",
		sellerID, sellerAmount, sellerBalance-sellerAmount, sellerBalance, orderID)

	var currentRating float64
	var currentReviews int
	database.DB.QueryRow("SELECT COALESCE(rating, 0), COALESCE(reviews, 0) FROM users WHERE id = $1", sellerID).Scan(&currentRating, &currentReviews)

	// Новый рейтинг
	database.DB.Exec("UPDATE users SET orders = orders + 1 WHERE id = $1", sellerID)

	// Проверяем и выдаем бейджи продавцу
	checkAndGiveBadges(sellerID)

	// Уведомления
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		sellerID, fmt.Sprintf("💰 Выплата %.0f ₽ по заказу #%s", sellerAmount, orderID), "/seller/orders")
	go sendPushNotification(sellerID, "Выплата", fmt.Sprintf("💰 +%.0f ₽", sellerAmount), "/seller/orders")

	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		buyerID, fmt.Sprintf("✅ Заказ #%s подтвержден", orderID), "/order/"+orderID)
	go sendPushNotification(buyerID, "Заказ подтверждён", fmt.Sprintf("✅ Заказ #%s", orderID), "/order/"+orderID)

	c.JSON(200, gin.H{"success": true, "message": "Заказ подтвержден! Средства переведены продавцу."})
}

// Возврат покупателю
func refundEscrow(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("orderId")

	// Проверяем что админ или покупатель
	_, isAdmin := session.Values["admin_id"]

	var buyerID int
	var amount float64
	database.DB.QueryRow("SELECT buyer_id, amount FROM escrow_transactions WHERE order_id = $1 AND status = 'frozen'", orderID).
		Scan(&buyerID, &amount)

	if buyerID != userID.(int) && !isAdmin {
		c.JSON(403, gin.H{"success": false, "message": "Нет прав на возврат"})
		return
	}

	// Возвращаем деньги покупателю
	database.DB.Exec("UPDATE escrow_transactions SET status = 'refunded', refunded_at = NOW() WHERE order_id = $1", orderID)
	database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", amount, buyerID)
	database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)

	// История транзакций покупателя
	var buyerBalance float64
	database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", buyerID).Scan(&buyerBalance)
	database.DB.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'refund',$2,$3,$4,'Возврат по заказу',$5)",
		buyerID, amount, buyerBalance-amount, buyerBalance, orderID)

	// Уведомление покупателю
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		buyerID,
		fmt.Sprintf("↩️ Возврат %.0f ₽ по заказу #%s", amount, orderID),
		"/escrow")

	c.JSON(200, gin.H{"success": true, "message": "Средства возвращены покупателю"})
}

// Страница истории транзакций
func transactionsPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	rows, _ := database.DB.Query(`
        SELECT id, type, amount, balance_before, balance_after, description, created_at 
        FROM transaction_history 
        WHERE user_id = $1 
        ORDER BY created_at DESC LIMIT 50
    `, userID)
	if rows != nil {
		defer rows.Close()
	}

	type Transaction struct {
		ID            int
		Type          string
		Amount        float64
		BalanceBefore float64
		BalanceAfter  float64
		Description   string
		CreatedAt     time.Time
	}

	var transactions []Transaction
	if rows != nil {
		for rows.Next() {
			var t Transaction
			rows.Scan(&t.ID, &t.Type, &t.Amount, &t.BalanceBefore, &t.BalanceAfter, &t.Description, &t.CreatedAt)
			transactions = append(transactions, t)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username, balance FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username, &user.Balance)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "transactions",
		"Data":   gin.H{"Transactions": transactions},
	})
}

func getTransactions(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	rows, _ := database.DB.Query(
		"SELECT id, type, amount, description, created_at FROM transaction_history WHERE user_id = $1 ORDER BY created_at DESC LIMIT 20",
		userID,
	)
	if rows != nil {
		defer rows.Close()
	}

	var transactions []gin.H
	if rows != nil {
		for rows.Next() {
			var id int
			var ttype, desc string
			var amount float64
			var createdAt time.Time
			rows.Scan(&id, &ttype, &amount, &desc, &createdAt)
			transactions = append(transactions, gin.H{"id": id, "type": ttype, "amount": amount, "description": desc, "created_at": createdAt})
		}
	}

	c.JSON(200, transactions)
}

// Функция:
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
func giveBadge(userID int, badgeType, badgeName, badgeIcon, badgeColor string) {
	database.DB.Exec("INSERT INTO user_badges (user_id, badge_type, badge_name, badge_icon, badge_color) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (user_id, badge_type) DO NOTHING",
		userID, badgeType, badgeName, badgeIcon, badgeColor)
}

// Получить бейджи пользователя
func getUserBadges(userID int) []gin.H {
	rows, _ := database.DB.Query("SELECT badge_name, badge_icon, badge_color FROM user_badges WHERE user_id = $1", userID)
	if rows != nil {
		defer rows.Close()
	}

	var badges []gin.H
	if rows != nil {
		for rows.Next() {
			var name, icon, color string
			rows.Scan(&name, &icon, &color)
			badges = append(badges, gin.H{"name": name, "icon": icon, "color": color})
		}
	}
	return badges
}

// API - верификация продавца админом
func adminVerifySellerAPI(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("id"))
	giveBadge(userID, "verified", "Верифицированный продавец", "✅", "#1cd698")
	c.JSON(200, gin.H{"success": true})
}

// Авто-выдача бейджей при достижениях
func checkAndGiveBadges(userID int) {
	var orders int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE booster_id = $1 AND status = 'completed'", userID).Scan(&orders)

	var rating float64
	database.DB.QueryRow("SELECT COALESCE(rating, 0) FROM users WHERE id = $1", userID).Scan(&rating)

	if orders >= 10 {
		giveBadge(userID, "pro", "Опытный продавец", "🌟", "#fbbf24")
	}
	if orders >= 50 {
		giveBadge(userID, "master", "Мастер продаж", "👑", "#ff6b6b")
	}
	if rating >= 4.8 {
		giveBadge(userID, "top-rated", "Топ-рейтинг", "⭐", "#f59e0b")
	}
}

// Страница заявки на верификацию
func verificationPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	// Проверяем есть ли уже заявка
	var pendingCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM verification_requests WHERE user_id = $1 AND status = 'pending'", userID).Scan(&pendingCount)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   &user,
		"Active": "verification",
		"Data":   gin.H{"HasPending": pendingCount > 0},
	})
}

// Подача заявки
func applyVerification(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	fullName := c.PostForm("full_name")
	description := c.PostForm("description")
	contact := c.PostForm("contact")

	database.DB.Exec(
		"INSERT INTO verification_requests (user_id, full_name, description, contact) VALUES ($1,$2,$3,$4)",
		userID, fullName, description, contact,
	)

	// Уведомление админам
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES (1, '📝 Новая заявка на верификацию!', '/admin/verifications')")

	c.Redirect(302, "/profile?success=Заявка+отправлена")
}

// Админка - заявки на верификацию
func adminVerificationsPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT v.id, v.user_id, u.username, v.full_name, v.description, v.contact, v.status, v.created_at 
        FROM verification_requests v 
        JOIN users u ON v.user_id = u.id 
        ORDER BY v.created_at DESC
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type VerificationRow struct {
		ID          int
		UserID      int
		Username    string
		FullName    string
		Description string
		Contact     string
		Status      string
		CreatedAt   time.Time
	}

	var verifications []VerificationRow
	if rows != nil {
		for rows.Next() {
			var v VerificationRow
			rows.Scan(&v.ID, &v.UserID, &v.Username, &v.FullName, &v.Description, &v.Contact, &v.Status, &v.CreatedAt)
			verifications = append(verifications, v)
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "admin-verifications",
		"User":   user,
		"Data":   gin.H{"Verifications": verifications},
	})
}

// Одобрить верификацию
func adminApproveVerification(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	comment := c.PostForm("comment")

	var userID int
	database.DB.QueryRow("SELECT user_id FROM verification_requests WHERE id = $1", id).Scan(&userID)

	database.DB.Exec("UPDATE verification_requests SET status = 'approved', admin_comment = $1, reviewed_at = NOW() WHERE id = $2", comment, id)

	// Выдаем бейдж
	giveBadge(userID, "verified", "Верифицированный Продавец", "✅", "#10b981")

	// Уведомление
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1, '✅ Ваша заявка на верификацию одобрена!', '/profile')", userID)

	c.Redirect(302, "/admin/verifications")
}

// Отклонить верификацию
func adminRejectVerification(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	comment := c.PostForm("comment")

	var userID int
	database.DB.QueryRow("SELECT user_id FROM verification_requests WHERE id = $1", id).Scan(&userID)

	database.DB.Exec("UPDATE verification_requests SET status = 'rejected', admin_comment = $1, reviewed_at = NOW() WHERE id = $2", comment, id)

	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1, '❌ Ваша заявка на верификацию отклонена', '/profile')", userID)

	c.Redirect(302, "/admin/verifications")
}

// Назначить роль
func adminAssignRole(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("id"))
	role := c.PostForm("role")

	database.DB.Exec("INSERT INTO user_roles (user_id, role, assigned_by) VALUES ($1,$2,1)", userID, role)

	c.Redirect(302, "/admin/users")
}

// Снять роль
func adminRemoveRole(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("id"))
	role := c.Query("role")

	database.DB.Exec("DELETE FROM user_roles WHERE user_id = $1 AND role = $2", userID, role)

	c.Redirect(302, "/admin/users")
}

// Страница редактирования профиля продавца
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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

	c.HTML(http.StatusOK, "layout.html", gin.H{
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
func forgotPasswordPage(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "forgot-password",
	})
}

func sendResetCode(c *gin.Context) {
	email := c.PostForm("email")

	var userID int
	var username string
	err := database.DB.QueryRow("SELECT id, username FROM users WHERE email = $1", email).Scan(&userID, &username)

	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"Active": "forgot-password",
			"Error":  "Пользователь с таким email не найден",
		})
		return
	}

	// Генерируем 6-значный код
	code := fmt.Sprintf("%06d", rand.Intn(1000000))

	// Сохраняем код (действителен 15 минут)
	database.DB.Exec(
		"INSERT INTO password_resets (user_id, code, expires_at) VALUES ($1, $2, NOW() + INTERVAL '15 minutes')",
		userID, code,
	)

	// Временно показываем код на странице
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active":    "forgot-password",
		"Success":   "Код отправлен на " + email,
		"ResetCode": code,
		"UserID":    userID,
	})
}

func resetPasswordPage(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"Active": "reset-password",
	})
}

func resetPassword(c *gin.Context) {
	email := c.PostForm("email")
	code := c.PostForm("code")
	newPassword := c.PostForm("password")

	var userID int
	err := database.DB.QueryRow("SELECT id FROM users WHERE email = $1", email).Scan(&userID)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"Active": "reset-password",
			"Error":  "Пользователь не найден",
		})
		return
	}

	// Проверяем код
	var valid bool
	database.DB.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM password_resets WHERE user_id = $1 AND code = $2 AND expires_at > NOW() AND used = false)",
		userID, code,
	).Scan(&valid)

	if !valid {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"Active": "reset-password",
			"Error":  "Неверный или истекший код",
		})
		return
	}

	// Меняем пароль
	hash, _ := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	database.DB.Exec("UPDATE users SET password = $1 WHERE id = $2", string(hash), userID)

	// Отмечаем код как использованный
	database.DB.Exec("UPDATE password_resets SET used = true WHERE user_id = $1 AND code = $2", userID, code)

	c.Redirect(302, "/login?success=Пароль+изменен!")
}

// ============ PUSH-УВЕДОМЛЕНИЯ ============

type pushSubscription struct {
	Endpoint  string `json:"endpoint"`
	Auth      string `json:"auth"`
	P256dh    string `json:"p256dh"`
	UserAgent string `json:"user_agent"`
}

func pushSubscribe(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "auth required"})
		return
	}

	body, _ := io.ReadAll(c.Request.Body)
	var sub pushSubscription
	json.Unmarshal(body, &sub)

	database.DB.Exec(`
        INSERT INTO push_subscriptions (user_id, endpoint, auth, p256dh, user_agent) 
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (user_id, endpoint) DO UPDATE SET auth=$3, p256dh=$4, updated_at=NOW()
    `, userID, sub.Endpoint, sub.Auth, sub.P256dh, sub.UserAgent)

	c.JSON(200, gin.H{"success": true})
}

func sendPushNotification(userID int, title, body, url string) {
	rows, err := database.DB.Query(
		"SELECT endpoint, auth, p256dh FROM push_subscriptions WHERE user_id = $1",
		userID,
	)
	if err != nil {
		log.Printf("Push query error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var endpoint, auth, p256dh string
		rows.Scan(&endpoint, &auth, &p256dh)

		s := &webpush.Subscription{
			Endpoint: endpoint,
			Keys: webpush.Keys{
				Auth:   auth,
				P256dh: p256dh,
			},
		}

		payload, _ := json.Marshal(map[string]string{
			"title": title,
			"body":  body,
			"url":   url,
		})

		_, err := webpush.SendNotification(payload, s, &webpush.Options{
			Subscriber:      "xsonebmp@example.com",
			VAPIDPublicKey:  vapidPublicKey,
			VAPIDPrivateKey: vapidPrivateKey,
			TTL:             30,
		})
		if err != nil {
			log.Printf("Push send error: %v", err)
			database.DB.Exec("DELETE FROM push_subscriptions WHERE endpoint = $1", endpoint)
		}
	}
}

// ============ QR-ВХОД ============

const qrSecret = "f42c8b535b814d28bfbfd060287fd938a6ab75c7ea9ac9c5af818eda1514bda0"

func qrGenerate(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}

	// Удаляем старые сессии
	database.DB.Exec("DELETE FROM qr_sessions WHERE user_id = $1", userID)

	token := randomString(32)
	expires := time.Now().Add(5 * time.Minute)

	// Создаём подпись: HMAC-SHA256(token, secret)
	mac := hmac.New(sha256.New, []byte(qrSecret))
	mac.Write([]byte(token))
	signature := hex.EncodeToString(mac.Sum(nil))

	database.DB.Exec(
		"INSERT INTO qr_sessions (token, user_id, ip, user_agent, expires_at, signature) VALUES ($1, $2, $3, $4, $5, $6)",
		token, userID.(int), c.ClientIP(), c.Request.UserAgent(), expires, signature,
	)

	// В QR-код передаём токен и подпись
	qrData := fmt.Sprintf("%s.%s", token, signature)
	c.JSON(200, gin.H{"token": qrData})
}

func qrCheckStatus(c *gin.Context) {
	token := c.Param("token")

	var confirmed, approved, scanned bool
	err := database.DB.QueryRow(
		"SELECT confirmed, COALESCE(approved, false), COALESCE(scanned, false) FROM qr_sessions WHERE token = $1 AND expires_at > NOW()",
		token,
	).Scan(&confirmed, &approved, &scanned)

	if err != nil {
		c.JSON(404, gin.H{"status": "expired"})
		return
	}

	if confirmed {
		c.JSON(200, gin.H{"status": "confirmed"})
	} else if approved {
		c.JSON(200, gin.H{"status": "approved"})
	} else if scanned {
		c.JSON(200, gin.H{"status": "scanned"}) // ← телефон отсканировал, ждёт подтверждения
	} else {
		c.JSON(200, gin.H{"status": "waiting"}) // ← QR сгенерирован, никто ещё не сканировал
	}
}

func qrConfirm(c *gin.Context) {
	var req struct {
		Token  string `json:"token"`
		Action string `json:"action"`
	}
	c.BindJSON(&req)

	if req.Token == "" {
		c.JSON(400, gin.H{"error": "Токен обязателен"})
		return
	}

	// Для approve (компьютер) оставляем старую логику
	if req.Action == "approve" {
		token := strings.SplitN(req.Token, ".", 2)[0] // берём только чистый токен
		_, err := database.DB.Exec("UPDATE qr_sessions SET approved = true WHERE token = $1", token)
		if err != nil {
			c.JSON(500, gin.H{"error": "Ошибка сервера"})
			return
		}
		c.JSON(200, gin.H{"success": true})
		return
	}

	// === LOGIN (телефон) ===
	// Разделяем токен и подпись
	parts := strings.SplitN(req.Token, ".", 2)
	if len(parts) != 2 {
		c.JSON(400, gin.H{"error": "Неверный формат токена"})
		return
	}
	token := parts[0]
	signature := parts[1]

	// Проверяем подпись
	mac := hmac.New(sha256.New, []byte(qrSecret))
	mac.Write([]byte(token))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		c.JSON(400, gin.H{"error": "Недействительный токен"})
		return
	}

	// Ищем сессию
	var userID int
	var username string
	var approved bool
	err := database.DB.QueryRow(
		`SELECT u.id, u.username, COALESCE(q.approved, false) FROM qr_sessions q 
         JOIN users u ON q.user_id = u.id 
         WHERE q.token = $1 AND q.expires_at > NOW() AND q.used = false`,
		token,
	).Scan(&userID, &username, &approved)

	if err != nil {
		c.JSON(404, gin.H{"error": "QR-код истёк"})
		return
	}

	// Отмечаем, что код был отсканирован
	database.DB.Exec("UPDATE qr_sessions SET scanned = true WHERE token = $1", token)

	if !approved {
		c.JSON(200, gin.H{"error": "Ожидание подтверждения", "waiting": true})
		return
	}

	// Вход разрешён
	database.DB.Exec(
		"UPDATE qr_sessions SET confirmed = true, used = true, confirmed_at = NOW() WHERE token = $1",
		token,
	)

	// Создаём сессию для телефона
	session, _ := store.Get(c.Request, "xsonebmp-session")
	session.Values["user_id"] = userID
	session.Values["username"] = username
	session.Values["session_uuid"] = randomString(32)
	session.Save(c.Request, c.Writer)

	c.JSON(200, gin.H{"success": true, "redirect": "/profile"})
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func qrReject(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	c.BindJSON(&req)

	// Удаляем токен
	database.DB.Exec(
		"DELETE FROM qr_sessions WHERE token = $1 AND user_id = $2",
		req.Token, userID,
	)

	c.JSON(200, gin.H{"success": true})
}
func referralPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	var refCode string
	err := database.DB.QueryRow("SELECT id, username, balance, COALESCE(referral_code, '') FROM users WHERE id = $1", userID).
		Scan(&user.ID, &user.Username, &user.Balance, &refCode)

	if err != nil {
		c.Redirect(302, "/login")
		return
	}

	// Если реферального кода нет — генерируем
	if refCode == "" {
		refCode = randomString(8)
		database.DB.Exec("UPDATE users SET referral_code = $1 WHERE id = $2", refCode, userID)
	}

	// Прямые рефералы
	var directCount int
	var directEarnings float64
	database.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(earnings), 0) FROM referrals WHERE referrer_id = $1 AND level = 1", userID).
		Scan(&directCount, &directEarnings)

	// Рефералы второго уровня
	var level2Count int
	var level2Earnings float64
	database.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(earnings), 0) FROM referrals WHERE referrer_id = $1 AND level = 2", userID).
		Scan(&level2Count, &level2Earnings)

	// История заработка
	earnings := []gin.H{}
	rows, err := database.DB.Query(`
		SELECT re.amount, re.level, re.created_at, u.username 
		FROM referral_earnings re 
		JOIN users u ON re.from_user_id = u.id 
		WHERE re.user_id = $1 
		ORDER BY re.created_at DESC LIMIT 20
	`, userID)

	if err == nil && rows != nil {
		defer rows.Close()
		for rows.Next() {
			var amount float64
			var level int
			var createdAt time.Time
			var username string
			rows.Scan(&amount, &level, &createdAt, &username)
			earnings = append(earnings, gin.H{
				"Amount":    amount,
				"Level":     level,
				"CreatedAt": createdAt,
				"Username":  username,
			})
		}
	}

	refLink := "https://xsonebmp.ru?ref=" + refCode

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":           &user,
		"Active":         "referral",
		"RefCode":        refCode,
		"RefLink":        refLink,
		"DirectCount":    directCount,
		"DirectEarnings": directEarnings,
		"Level2Count":    level2Count,
		"Level2Earnings": level2Earnings,
		"Earnings":       earnings,
	})
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

var httpClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
	},
}

func getLocationByIP(ip string) string {
	if ip == "::1" || ip == "127.0.0.1" {
		return "Локально"
	}

	resp, err := httpClient.Get(fmt.Sprintf("http://ip-api.com/json/%s?fields=city,country", ip))
	if err != nil {
		return "Неизвестно"
	}
	defer resp.Body.Close()

	var result struct {
		City    string `json:"city"`
		Country string `json:"country"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.City != "" {
		return result.City + ", " + result.Country
	}
	return "Неизвестно"
}
func sessionsPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).
		Scan(&user.ID, &user.Username)

	rows, _ := database.DB.Query(`
        SELECT id, session_token, ip, user_agent, COALESCE(location, ''), COALESCE(device, ''), is_active, created_at, last_seen 
        FROM user_sessions WHERE user_id = $1 AND revoked = false
        ORDER BY last_seen DESC LIMIT 20
    `, userID)
	defer rows.Close()

	type SessionInfo struct {
		ID           int
		SessionToken string
		IP           string
		UA           string
		Location     string
		Device       string
		IsActive     bool
		CreatedAt    time.Time
		LastSeen     time.Time
	}

	var sessions []SessionInfo
	for rows.Next() {
		var s SessionInfo
		rows.Scan(&s.ID, &s.SessionToken, &s.IP, &s.UA, &s.Location, &s.Device, &s.IsActive, &s.CreatedAt, &s.LastSeen)
		sessions = append(sessions, s)
	}

	currentUUID, _ := session.Values["session_uuid"].(string)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":               &user,
		"Active":             "sessions",
		"Sessions":           sessions,
		"CurrentSessionUUID": currentUUID,
	})
}

func getDeviceInfo(ua string) string {
	ua = strings.ToLower(ua)
	if strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") {
		return "📱 iPhone"
	}
	if strings.Contains(ua, "android") {
		return "📱 Android"
	}
	if strings.Contains(ua, "windows") {
		return "💻 Windows"
	}
	if strings.Contains(ua, "mac") {
		return "💻 Mac"
	}
	return "🖥️ Устройство"
}

func revokeSession(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	sessionID := c.Param("id")
	database.DB.Exec("UPDATE user_sessions SET revoked = true WHERE id = $1 AND user_id = $2", sessionID, userID)

	// Если завершаем текущую сессию – разлогиниваем
	currentUUID, _ := session.Values["session_uuid"].(string)
	var revokedUUID string
	database.DB.QueryRow("SELECT session_token FROM user_sessions WHERE id = $1", sessionID).Scan(&revokedUUID)
	if currentUUID == revokedUUID {
		session.Values = make(map[interface{}]interface{})
		session.Save(c.Request, c.Writer)
		c.Redirect(302, "/login")
		return
	}

	c.Redirect(302, "/sessions?success=Сессия+завершена")
}

var (
	cachedStats     map[string]interface{}
	cachedStatsTime time.Time
	statsMutex      sync.Mutex
)

func getCachedStats() map[string]interface{} {
	statsMutex.Lock()
	defer statsMutex.Unlock()

	if time.Since(cachedStatsTime) < 5*time.Minute && cachedStats != nil {
		return cachedStats
	}

	var usersCount, ordersCount int
	var avgRating float64
	database.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&usersCount)
	database.DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&ordersCount)
	database.DB.QueryRow("SELECT COALESCE(AVG(rating), 0) FROM users WHERE rating > 0").Scan(&avgRating)

	cachedStats = map[string]interface{}{
		"users":  usersCount,
		"orders": ordersCount,
		"rating": avgRating,
	}
	cachedStatsTime = time.Now()
	return cachedStats
}

var (
	settingsCache   map[string]string
	settingsCacheMu sync.RWMutex
)

func loadSettingsToCache() {
	settingsCacheMu.Lock()
	defer settingsCacheMu.Unlock()
	settingsCache = loadAllSettings() // ваша существующая функция
}

// Вместо прямых запросов к БД в middleware используйте:
func getCachedSetting(key string) string {
	settingsCacheMu.RLock()
	defer settingsCacheMu.RUnlock()
	if val, ok := settingsCache[key]; ok {
		return val
	}
	return ""
}
func checkRevokedSession(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	sessionUUID, ok := session.Values["session_uuid"].(string)
	if !ok || sessionUUID == "" {
		c.Next()
		return
	}

	var revoked bool
	err := database.DB.QueryRow("SELECT revoked FROM user_sessions WHERE session_token = $1", sessionUUID).Scan(&revoked)
	if err == nil && revoked {
		session.Values = make(map[interface{}]interface{})
		session.Save(c.Request, c.Writer)
		c.Redirect(302, "/login")
		c.Abort()
		return
	}
	c.Next()
}

func adminSettingsPage(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil {
		c.Redirect(302, "/admin/login")
		return
	}
	u := user.(*User)
	if u.ID != 1 {
		c.Redirect(302, "/")
		return
	}

	settings := loadAllSettings()
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":     user,
		"Active":   "admin-settings",
		"Settings": settings,
	})
}
func loadAllSettings() map[string]string {
	rows, err := database.DB.Query("SELECT key, value FROM settings")
	if err != nil {
		// если таблицы ещё нет – создадим позже, но пока вернём дефолты
		return getDefaultSettings()
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, val string
		rows.Scan(&key, &val)
		settings[key] = val
	}

	// Заполняем недостающие ключи значениями по умолчанию
	defaults := getDefaultSettings()
	for k, v := range defaults {
		if _, ok := settings[k]; !ok {
			settings[k] = v
			// сразу сохраняем в БД, чтобы потом не проверять каждый раз
			database.DB.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, k, v)
		}
	}
	return settings
}

func getDefaultSettings() map[string]string {
	return map[string]string{
		"site_name":            "XSoneBMP",
		"site_description":     "Маркетплейс игровых услуг",
		"site_keywords":        "boost, mmr, игра, услуги",
		"contact_email":        "support@xsonebmp.ru",
		"platform_fee":         "5",
		"min_withdraw":         "100",
		"currency":             "₽",
		"referral_percent":     "5",
		"referral_level2":      "2",
		"session_hours":        "720",
		"max_orders_per_day":   "0",
		"enable_registration":  "true",
		"recaptcha_site_key":   "",
		"recaptcha_secret_key": "",
		"smtp_host":            "",
		"smtp_port":            "587",
		"smtp_user":            "",
		"smtp_password":        "",
		"smtp_from":            "",
		"ga_id":                "",
		"ym_id":                "",
		"telegram_bot_token":   "",
		"telegram_chat_id":     "",
		"webhook_url":          "",
		"logo_url":             "/static/logo.png",
		"favicon_url":          "/static/favicon.ico",
		"primary_color":        "#ef4444",
		"secondary_color":      "#8b5cf6",
		"cache_timeout":        "300",
		"maintenance_mode":     "false",
		"maintenance_message":  "Технические работы. Скоро вернемся!",
	}
}

func adminUploadLogo(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil || user.(*User).ID != 1 {
		c.JSON(403, gin.H{"error": "Access denied"})
		return
	}

	file, err := c.FormFile("logo")
	if err != nil {
		c.JSON(400, gin.H{"error": "Файл не загружен"})
		return
	}

	// Проверка расширения
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		c.JSON(400, gin.H{"error": "Разрешены только PNG, JPG, JPEG"})
		return
	}

	// Генерируем уникальное имя, чтобы избежать кэширования
	filename := fmt.Sprintf("logo_%d%s", time.Now().Unix(), ext)
	dst := "./static/" + filename
	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(500, gin.H{"error": "Ошибка сохранения"})
		return
	}

	logoURL := "/static/" + filename
	database.DB.Exec(`INSERT INTO settings (key, value) VALUES ('logo_url', $1) ON CONFLICT (key) DO UPDATE SET value = $1`, logoURL)

	c.JSON(200, gin.H{"success": true, "url": logoURL})
}

func adminUploadFavicon(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil || user.(*User).ID != 1 {
		c.JSON(403, gin.H{"error": "Access denied"})
		return
	}

	file, err := c.FormFile("favicon")
	if err != nil {
		c.JSON(400, gin.H{"error": "Файл не загружен"})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".ico" && ext != ".png" {
		c.JSON(400, gin.H{"error": "Разрешены только .ico или .png"})
		return
	}

	filename := "favicon.ico" // можно всегда так называть, чтобы браузер подхватил
	if ext == ".png" {
		filename = "favicon.png"
	}
	dst := "./static/" + filename
	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(500, gin.H{"error": "Ошибка сохранения"})
		return
	}

	faviconURL := "/static/" + filename
	database.DB.Exec(`INSERT INTO settings (key, value) VALUES ('favicon_url', $1) ON CONFLICT (key) DO UPDATE SET value = $1`, faviconURL)

	c.JSON(200, gin.H{"success": true})
}
func adminResetSettings(c *gin.Context) {
	user, _ := c.Get("user")
	if user == nil || user.(*User).ID != 1 {
		c.JSON(403, gin.H{"error": "Доступ запрещён"})
		return
	}

	defaults := getDefaultSettings()
	for key, val := range defaults {
		database.DB.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = $2`, key, val)
	}

	// Очищаем историю изменений (опционально)
	// database.DB.Exec("DELETE FROM settings_history")

	c.JSON(200, gin.H{"success": true})
}

func isMaintenanceMode() bool {
	var val string
	err := database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_mode'").Scan(&val)
	if err != nil {
		return false // при ошибке считаем, что режим выключен
	}
	return val == "true"
}

func getMaintenanceMessage() string {
	var msg string
	err := database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_message'").Scan(&msg)
	if err != nil || msg == "" {
		return "Технические работы. Скоро вернемся!"
	}
	return msg
}
func maintenanceMiddleware(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, "/static/") ||
		strings.HasPrefix(c.Request.URL.Path, "/admin") ||
		strings.HasPrefix(c.Request.URL.Path, "/api/admin") ||
		c.Request.URL.Path == "/maintenance" {
		c.Next()
		return
	}

	session, _ := store.Get(c.Request, "xsonebmp-session")
	isAdmin := false
	if userID, ok := session.Values["user_id"]; ok && userID == 1 {
		isAdmin = true
	}
	if isAdmin {
		c.Next()
		return
	}

	var mode string
	database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_mode'").Scan(&mode)
	if mode == "true" {
		var message, reason, endTimeStr string
		database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_message'").Scan(&message)
		database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_reason'").Scan(&reason)
		database.DB.QueryRow("SELECT value FROM settings WHERE key = 'maintenance_end_time'").Scan(&endTimeStr)

		c.HTML(http.StatusServiceUnavailable, "layout.html", gin.H{
			"Active":             "maintenance",
			"Message":            message,
			"Reason":             reason,
			"MaintenanceEndTime": endTimeStr,
			"User":               nil,
		})
		c.Abort()
		return
	}
	c.Next()
}
func adminEditUserPage(c *gin.Context) {
	if !isAdmin(c) {
		c.Redirect(302, "/admin/login")
		return
	}

	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.String(400, "Неверный ID пользователя")
		return
	}

	var u User
	err = database.DB.QueryRow(
		"SELECT id, username, email, balance, level, orders FROM users WHERE id = $1",
		userID,
	).Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Level, &u.Orders)

	if err != nil {
		c.String(404, "Пользователь не найден")
		return
	}

	var isBanned bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM banned_users WHERE user_id = $1)", userID).Scan(&isBanned)

	currentUser, _ := c.Get("user")

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"User":   currentUser,
		"Active": "admin-edit-user",
		"Data": gin.H{
			"EditUser": u,
			"IsBanned": isBanned,
		},
	})
}
func changePassword(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	currentPassword := c.PostForm("current_password")
	newPassword := c.PostForm("new_password")
	confirmPassword := c.PostForm("confirm_password")

	// Проверки
	if currentPassword == "" || newPassword == "" || confirmPassword == "" {
		render(c, "edit-profile", gin.H{"Error": "Все поля пароля обязательны"})
		return
	}
	if newPassword != confirmPassword {
		render(c, "edit-profile", gin.H{"Error": "Новый пароль и подтверждение не совпадают"})
		return
	}
	if len(newPassword) < 8 {
		render(c, "edit-profile", gin.H{"Error": "Новый пароль должен быть не менее 8 символов"})
		return
	}

	// Проверяем текущий пароль
	var hashedPassword string
	err := database.DB.QueryRow("SELECT password FROM users WHERE id = $1", userID).Scan(&hashedPassword)
	if err != nil {
		render(c, "edit-profile", gin.H{"Error": "Ошибка сервера"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(currentPassword)) != nil {
		render(c, "edit-profile", gin.H{"Error": "Неверный текущий пароль"})
		return
	}

	// Хешируем и сохраняем новый пароль
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		render(c, "edit-profile", gin.H{"Error": "Ошибка сервера"})
		return
	}
	_, err = database.DB.Exec("UPDATE users SET password = $1 WHERE id = $2", string(newHash), userID)
	if err != nil {
		render(c, "edit-profile", gin.H{"Error": "Ошибка при смене пароля"})
		return
	}

	render(c, "edit-profile", gin.H{"Success": "Пароль успешно изменён!"})
}
