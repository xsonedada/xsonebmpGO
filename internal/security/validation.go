package security

import (
    "regexp"
    "strings"
    "html"
    "unicode/utf8"
    "strconv"
)

var (
    SafeTextPattern   = regexp.MustCompile(`^[\p{L}\p{N}\s\-_.,!?()\[\]{}:;@#$%^&*+=|\\/~]+$`)
    UsernamePattern   = regexp.MustCompile(`^[a-zA-Z0-9_\-]{3,32}$`)
    EmailPattern      = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
    URLPattern        = regexp.MustCompile(`^https?://[^\s/$.?#].[^\s]*$`)
    NumericPattern    = regexp.MustCompile(`^-?\d+\.?\d*$`)
)

// InputValidator общая структура валидатора
type InputValidator struct {
    MaxLength     int
    Pattern       *regexp.Regexp
    StripHTML     bool
    StripScripts  bool
}

// NewGenericValidator создает валидатор для текста
func NewGenericValidator(maxLen int) *InputValidator {
    return &InputValidator{
        MaxLength:    maxLen,
        Pattern:      SafeTextPattern,
        StripHTML:    true,
        StripScripts: true,
    }
}

// NewUsernameValidator для никнеймов
func NewUsernameValidator() *InputValidator {
    return &InputValidator{
        MaxLength:    32,
        Pattern:      UsernamePattern,
        StripHTML:    true,
        StripScripts: true,
    }
}

// NewEmailValidator для email
func NewEmailValidator() *InputValidator {
    return &InputValidator{
        MaxLength:    255,
        Pattern:      EmailPattern,
        StripHTML:    true,
        StripScripts: true,
    }
}

// Validate проверяет и очищает строку
func (v *InputValidator) Validate(input string) (string, error) {
    if input == "" {
        return "", ErrEmptyInput
    }
    
    // Проверка длины
    if utf8.RuneCountInString(input) > v.MaxLength {
        return "", ErrTooLong
    }
    
    // Очистка от HTML
    if v.StripHTML {
        input = StripHTML(input)
    }
    
    // Очистка от скриптов
    if v.StripScripts {
        input = StripScripts(input)
    }
    
    // Проверка паттерна
    if v.Pattern != nil && !v.Pattern.MatchString(input) {
        return "", ErrInvalidFormat
    }
    
    return strings.TrimSpace(input), nil
}

// ValidatePrice проверяет цену
func ValidatePrice(input string) (float64, error) {
    if !NumericPattern.MatchString(input) {
        return 0, ErrInvalidPrice
    }
    
    price, err := strconv.ParseFloat(input, 64)
    if err != nil {
        return 0, ErrInvalidPrice
    }
    
    if price < 0 || price > 1000000 {
        return 0, ErrInvalidPrice
    }
    
    return price, nil
}

// ValidateOrderID проверяет ID заказа
func ValidateOrderID(input string) (int, error) {
    id, err := strconv.Atoi(input)
    if err != nil {
        return 0, ErrInvalidID
    }
    if id < 0 {
        return 0, ErrInvalidID
    }
    return id, nil
}

// Экранирование HTML
func EscapeHTML(input string) string {
    return html.EscapeString(input)
}

// StripHTML удаляет все HTML-теги
func StripHTML(input string) string {
    // Удаляем все теги
    re := regexp.MustCompile(`<[^>]*>`)
    return re.ReplaceAllString(input, "")
}

// StripScripts удаляет JavaScript-инъекции
func StripScripts(input string) string {
    dangerous := []string{
        "<script", "</script>",
        "javascript:", "vbscript:",
        "onerror=", "onload=", "onclick=", "onmouseover=",
        "eval(", "expression(",
        "document.cookie", "document.write",
        "data:text/html",
        "&#", "\\x",
    }
    
    cleaned := input
    for _, d := range dangerous {
        cleaned = strings.ReplaceAll(
            strings.ToLower(cleaned), 
            strings.ToLower(d), 
            "",
        )
    }
    
    return cleaned
}