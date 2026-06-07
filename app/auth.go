package main

import (
	"log"
	"net/http"
	"nexus-boost/database"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

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

	// Honeypot
	if c.PostForm("website") != "" {
		c.Redirect(302, "/")
		return
	}
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
		"INSERT INTO users (username, email, password, balance) VALUES ($1, $2, $3, $4) RETURNING id",
		username, email, string(hash), startingBalance,
	).Scan(&userID)

	if err != nil {
		log.Printf("register insert error: %v", err)
		render(c, "register", gin.H{"Error": "Ошибка регистрации. Попробуйте другие данные."})
		return
	}
	database.DB.Exec("DELETE FROM user_sessions WHERE user_id = $1", userID)

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

	session, err = regenerateSession(c)
	if err != nil {
		render(c, "register", gin.H{"Error": "Ошибка сервера"})
		return
	}
	delete(session.Values, "csrf_token")
	session.Values["user_id"] = userID
	session.Values["username"] = username
	session.Values["session_uuid"] = randomString(32)
	session.Save(c.Request, c.Writer)

	c.Redirect(302, "/profile")
}

func isValidDomain(email string) bool {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	domain := strings.ToLower(parts[1])

	if domain == "localhost" || domain == "127.0.0.1" {
		return false
	}

	domainRe := regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9\-]*[a-z0-9])?)+$`)
	return domainRe.MatchString(domain)
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

	database.DB.Exec("DELETE FROM user_sessions WHERE user_id = $1", u.ID)

	session, err := regenerateSession(c)
	if err != nil {
		render(c, "login", gin.H{"Error": "Ошибка сервера"})
		return
	}
	session.Values["session_uuid"] = randomString(32)
	session.Values["user_id"] = u.ID
	session.Values["username"] = u.Username
	delete(session.Values, "csrf_token")
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

func forgotPasswordPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	csrfToken := randomString(32)
	session.Values["csrf_token"] = csrfToken
	session.Save(c.Request, c.Writer)
	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "forgot-password",
		"csrf":   csrfToken,
	})
}

func sendResetCode(c *gin.Context) {
	if !validateUserCSRF(c) {
		layoutHTML(c, http.StatusOK, gin.H{"Active": "forgot-password", "Error": "Ошибка безопасности. Обновите страницу."})
		return
	}
	if !rateLimitIP(c, resetAttempts, &resetMu, 3, time.Hour) {
		layoutHTML(c, http.StatusOK, gin.H{"Active": "forgot-password", "Error": "Слишком много попыток. Попробуйте позже."})
		return
	}

	email := strings.TrimSpace(c.PostForm("email"))
	genericSuccess := "Если аккаунт с таким email существует, инструкции отправлены на почту"

	var userID int
	err := database.DB.QueryRow("SELECT id FROM users WHERE email = $1", email).Scan(&userID)
	if err == nil {
		code := secureRandomCode6()
		database.DB.Exec("DELETE FROM password_resets WHERE user_id = $1", userID)
		database.DB.Exec(
			"INSERT INTO password_resets (user_id, code, expires_at) VALUES ($1, $2, NOW() + INTERVAL '15 minutes')",
			userID, code,
		)
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active":  "forgot-password",
		"Success": genericSuccess,
	})
}

func resetPasswordPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	csrfToken := randomString(32)
	session.Values["csrf_token"] = csrfToken
	session.Save(c.Request, c.Writer)
	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "reset-password",
		"csrf":   csrfToken,
	})
}

func resetPassword(c *gin.Context) {
	if !validateUserCSRF(c) {
		layoutHTML(c, http.StatusOK, gin.H{"Active": "reset-password", "Error": "Ошибка безопасности. Обновите страницу."})
		return
	}
	if !rateLimitIP(c, resetAttempts, &resetMu, 5, 15*time.Minute) {
		layoutHTML(c, http.StatusOK, gin.H{"Active": "reset-password", "Error": "Слишком много попыток. Подождите."})
		return
	}

	email := strings.TrimSpace(c.PostForm("email"))
	code := strings.TrimSpace(c.PostForm("code"))
	newPassword := c.PostForm("password")

	if len(newPassword) < 8 || !containsLetterAndDigit(newPassword) {
		layoutHTML(c, http.StatusOK, gin.H{"Active": "reset-password", "Error": "Пароль должен быть от 8 символов и содержать буквы и цифры"})
		return
	}

	var userID int
	err := database.DB.QueryRow("SELECT id FROM users WHERE email = $1", email).Scan(&userID)
	if err != nil {
		layoutHTML(c, http.StatusOK, gin.H{"Active": "reset-password", "Error": "Неверный или истекший код"})
		return
	}

	var valid bool
	database.DB.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM password_resets WHERE user_id = $1 AND code = $2 AND expires_at > NOW() AND used = false)",
		userID, code,
	).Scan(&valid)

	if !valid {
		layoutHTML(c, http.StatusOK, gin.H{"Active": "reset-password", "Error": "Неверный или истекший код"})
		return
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	database.DB.Exec("UPDATE users SET password = $1 WHERE id = $2", string(hash), userID)
	database.DB.Exec("UPDATE password_resets SET used = true WHERE user_id = $1 AND code = $2", userID, code)
	database.DB.Exec("DELETE FROM user_sessions WHERE user_id = $1", userID)

	c.Redirect(302, "/login?success=Пароль+изменен!")
}

// ============ PUSH-УВЕДОМЛЕНИЯ ============

func isValidImageHeader(header []byte) bool {
	if len(header) >= 4 && header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4E && header[3] == 0x47 {
		return true
	}
	if len(header) >= 3 && header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF {
		return true
	}
	return false
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
