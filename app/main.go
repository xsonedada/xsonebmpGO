package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"nexus-boost/database"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
	"github.com/joho/godotenv"
)

func main() {
	// Загружаем .env (в production переменные задаются через systemd/docker, ошибка игнорируется)
	_ = godotenv.Load()

	vapidPublicKey = mustEnv("VAPID_PUBLIC_KEY")
	vapidPrivateKey = mustEnv("VAPID_PRIVATE_KEY")
	adminUsername = mustEnv("ADMIN_USERNAME")
	adminPassHash = mustEnv("ADMIN_PASSWORD_HASH")
	sessionSecret = mustEnv("SESSION_SECRET")
	qrSecret = mustEnv("QR_SECRET")
	startingBalance = envFloat("STARTING_BALANCE", 0)
	cookieSecure = envBool("COOKIE_SECURE", false)

	if strings.EqualFold(os.Getenv("GIN_MODE"), "release") {
		gin.SetMode(gin.ReleaseMode)
	}

	// Инициализируем store здесь, после загрузки секрета
	store = sessions.NewCookieStore([]byte(sessionSecret))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   cookieSecure,
		SameSite: http.SameSiteStrictMode,
	}

	database.InitDB()
	initPreparedStatements()
	loadSettingsToCache()

	r := gin.Default()
	r.SetFuncMap(template.FuncMap{
		"list": func(items ...interface{}) []interface{} { return items },
	})
	r.LoadHTMLGlob("templates/*.html")

	staticGroup := r.Group("/static")
	staticGroup.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Next()
	})
	staticGroup.Static("", "./static")

	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "camera=self, microphone=(), geolocation=()")
		c.Next()
	})

	r.Use(userCSRFMiddleware)

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
		layoutHTML(c, http.StatusOK, gin.H{"User": user, "Active": "offline"})
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
			c.Next()
			return
		}
		var blockedUserAgents = []string{
			"sqlmap", "nikto", "nmap", "masscan", "zgrab",
		}
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

		isNewSession := false
		sessionUUID, _ := session.Values["session_uuid"].(string)
		if sessionUUID == "" {
			sessionUUID = randomString(32)
			session.Values["session_uuid"] = sessionUUID
			session.Save(c.Request, c.Writer)
			isNewSession = true
		}

		trackUserSessionAsync(sessionUUID, uid, ip, ua, device, isNewSession)

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
	r.POST("/seller/template/:id/delete", deleteTemplate)
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
		layoutHTML(c, http.StatusOK, gin.H{"User": user, "Active": "qr-scan"})
	})
	r.GET("/qr/confirm-page", func(c *gin.Context) {
		user, _ := c.Get("user")
		layoutHTML(c, http.StatusOK, gin.H{
			"User":   user,
			"Active": "qr-confirm",
			"Token":  c.Query("token"),
		})
	})
	r.GET("/qr-generate", func(c *gin.Context) {
		user, _ := c.Get("user")
		layoutHTML(c, http.StatusOK, gin.H{"User": user, "Active": "qr-generate"})
	})
	r.GET("/qr-login", func(c *gin.Context) {
		user, _ := c.Get("user")
		layoutHTML(c, http.StatusOK, gin.H{"User": user, "Active": "qr-login"})
	})
	r.POST("/qr/reject", qrReject)
	r.POST("/qr/scanned", qrScanned)

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
		session, _ := store.Get(c.Request, "xsonebmp-session")
		userID, ok := session.Values["user_id"]
		if !ok {
			c.JSON(403, gin.H{"error": "auth required"})
			return
		}
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		c.BindJSON(&req)
		if req.Endpoint != "" {
			database.DB.Exec("DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2", req.Endpoint, userID)
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
	r.GET("/api/dispute/:id/messages", getDisputeMessages)
	r.POST("/api/escrow/refund/:orderId", escrowRefund)
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
	r.POST("/support/ticket/:id/close", closeTicket)

	// ----------------------------------------------------------------
	// Админка — публичные маршруты (без авторизации)
	// ----------------------------------------------------------------
	r.GET("/admin/login", AdminLoginPage)
	r.POST("/admin/login", AdminLogin)
	r.POST("/admin/logout", adminRequired, adminCSRFMiddleware, AdminLogout)

	// ----------------------------------------------------------------
	// Админка — все защищённые маршруты под middleware
	// ----------------------------------------------------------------
	admin := r.Group("/admin", adminRequired, adminCSRFMiddleware)
	{
		admin.GET("", AdminDashboard)
		admin.GET("/users", AdminUsersPage)
		admin.GET("/orders", AdminOrdersPage)
		admin.POST("/order/:id/status", AdminUpdateOrderStatus)
		admin.POST("/order/:id/delete", AdminDeleteOrder)
		admin.GET("/order/:id", adminOrderDetail)
		admin.GET("/boosts", AdminBoostsPage)
		admin.POST("/boost/:id/delete", AdminDeleteBoost)
		admin.GET("/boost/:id/edit", adminEditBoostPage)
		admin.POST("/boost/:id/edit", adminEditBoost)
		admin.GET("/reviews", adminReviewsPage)
		admin.POST("/review/:id/delete", adminDeleteReview)
		admin.GET("/notify", adminNotifyPage)
		admin.POST("/notify/send", adminSendNotify)
		admin.POST("/user/:id/balance", adminUpdateBalance)
		admin.GET("/user/:id", adminEditUserPage)
		admin.POST("/user/:id", AdminEditUser)
		admin.GET("/messages/:id", adminViewMessages)
		admin.POST("/user/:id/ban", adminBanUser)
		admin.POST("/user/:id/unban", adminUnbanUser)
		admin.POST("/user/:id/make-pro", adminMakePro)
		admin.POST("/user/:id/role", adminAssignRole)
		admin.POST("/user/:id/role/remove", adminRemoveRole)
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
		admin.POST("/verify/:id", adminVerifySeller)
		admin.POST("/feature/:id", adminFeatureBoost)
		admin.GET("/promocodes", adminPromocodesPage)
		admin.POST("/promocode/create", adminCreatePromocode)
		admin.POST("/promocode/:id/delete", adminDeletePromocode)
		admin.POST("/promocode/:id/toggle", adminTogglePromocode)
		admin.GET("/sales", adminSalesPage)
		admin.POST("/sale/create", adminCreateSale)
		admin.POST("/sale/:id/toggle", adminToggleSale)
		admin.POST("/sale/:id/delete", adminDeleteSale)
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
	SellerOrders int
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
