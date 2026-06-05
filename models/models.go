// models/models.go
package models

import "time"

// User - основная структура пользователя
type User struct {
	ID          string    `json:"id" db:"id"`
	Username    string    `json:"username" db:"username"`
	Email       string    `json:"email" db:"email"`
	Password    string    `json:"-" db:"password"`
	IsPro       bool      `json:"is_pro" db:"is_pro"`
	ProUntil    time.Time `json:"pro_until" db:"pro_until"`
	Rating      float64   `json:"rating" db:"rating"`
	Reviews     int       `json:"reviews" db:"reviews"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	Bio         string    `json:"bio" db:"bio"`
	CustomColor string    `json:"custom_color" db:"custom_color"`

	// === PRO поля (ДОБАВЬТЕ СЮДА) ===
	ProSettings ProSettings `json:"pro_settings" db:"pro_settings"`
}

// ProSettings - настройки PRO профиля
type ProSettings struct {
	// Внешний вид
	Theme          string `json:"theme"`
	AccentColor    string `json:"accent_color"`
	AccentRGB      string `json:"accent_rgb"`
	AccentStat     string `json:"accent_stat"`
	AnimationLevel string `json:"animation_level"`
	CardRadius     string `json:"card_radius"`

	// Медиа
	AvatarURL          string `json:"avatar_url"`
	BackgroundImage    string `json:"background_image"`
	BackgroundGradient string `json:"background_gradient"`

	// Контент
	CustomBadges []CustomBadge   `json:"custom_badges"`
	Portfolio    []PortfolioItem `json:"portfolio"`
	SocialLinks  []SocialLink    `json:"social_links"`
	CustomFooter string          `json:"custom_footer"`
	CustomCSS    string          `json:"custom_css"`
	CustomJS     string          `json:"custom_js"`

	// Настройки видимости
	HideStats      []string `json:"hide_stats"`
	HideReviews    bool     `json:"hide_reviews"`
	ShowOnlineOnly bool     `json:"show_online_only"`

	// Дополнительно
	MarqueText     string `json:"marque_text"`
	WelcomeMessage string `json:"welcome_message"`
	VoiceGreeting  string `json:"voice_greeting"`
}

type CustomBadge struct {
	Emoji   string `json:"emoji"`
	Text    string `json:"text"`
	Color   string `json:"color"`
	Tooltip string `json:"tooltip"`
}

type PortfolioItem struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Thumbnail string `json:"thumbnail"`
	VideoURL  string `json:"video_url"`
}

type SocialLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Icon string `json:"icon"`
}

// Boost - структура товара (если есть)
type Boost struct {
	ID          string    `json:"id" db:"id"`
	SellerID    string    `json:"seller_id" db:"seller_id"`
	Title       string    `json:"title" db:"title"`
	Description string    `json:"description" db:"description"`
	Game        string    `json:"game" db:"game"`
	Price       float64   `json:"price" db:"price"`
	Rating      float64   `json:"rating" db:"rating"`
	Reviews     int       `json:"reviews" db:"reviews"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// Review - структура отзыва
type Review struct {
	ID        string    `json:"id" db:"id"`
	SellerID  string    `json:"seller_id" db:"seller_id"`
	Username  string    `json:"username" db:"username"`
	Rating    int       `json:"rating" db:"rating"`
	Text      string    `json:"text" db:"text"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
