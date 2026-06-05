package security

import (
    "html/template"
    "strings"
    "regexp"
)

// SanitizeHTML очищает HTML от XSS, оставляя безопасные теги
func SanitizeHTML(input string) template.HTML {
    // Удаляем все HTML теги кроме безопасных
    cleaned := StripHTML(input)
    cleaned = EscapeHTML(cleaned)
    return template.HTML(cleaned)
}

// SanitizeDescription для описаний с форматированием
func SanitizeDescription(input string) template.HTML {
    // Разрешаем только базовые теги
    allowedTags := []string{"b", "i", "u", "br", "p", "ul", "ol", "li"}
    
    cleaned := input
    // Удаляем все теги кроме разрешенных
    for _, tag := range allowedTags {
        // Сохраняем открывающие и закрывающие теги
        cleaned = strings.ReplaceAll(cleaned, "<"+tag+">", "&lt;"+tag+"&gt;")
        cleaned = strings.ReplaceAll(cleaned, "</"+tag+">", "&lt;/"+tag+"&gt;")
    }
    
    // Удаляем все остальные теги
    re := regexp.MustCompile(`<[^>]*>`)
    cleaned = re.ReplaceAllString(cleaned, "")
    
    // Восстанавливаем разрешенные теги
    cleaned = strings.ReplaceAll(cleaned, "&lt;", "<")
    cleaned = strings.ReplaceAll(cleaned, "&gt;", ">")
    
    return template.HTML(cleaned)
}

// SanitizeMessage для сообщений чата
func SanitizeMessage(input string) template.HTML {
    // В сообщениях разрешаем только текст и базовое форматирование
    cleaned := StripScripts(input)
    cleaned = EscapeHTML(cleaned)
    
    // Разрешаем переводы строк
    cleaned = strings.ReplaceAll(cleaned, "\n", "<br>")
    
    return template.HTML(cleaned)
}

// SafeURL проверяет что URL безопасный
func SafeURL(rawURL string) string {
    rawURL = strings.TrimSpace(rawURL)
    
    // Разрешаем только http/https
    if strings.HasPrefix(rawURL, "https://") || 
       strings.HasPrefix(rawURL, "http://") {
        return rawURL
    }
    
    // Относительные URL
    if strings.HasPrefix(rawURL, "/") {
        return rawURL
    }
    
    return "#"
}