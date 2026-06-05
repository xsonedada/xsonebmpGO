// static/js/ai-assistant.js — XSoneBMP AI Assistant ULTIMATE FINAL
(function() {
  'use strict';

  const CONFIG = {
    cacheMs: 45000,
    maxCards: 4,
    typingDelayMin: 350,
    typingDelayMax: 750,
    conversationMemory: 25,
    maxRecentSearches: 10
  };

  let marketData = [];
  let platformStats = null;
  let lastMarketFetch = 0;
  let lastStatsFetch = 0;
  let conversationHistory = [];
  let userPreferences = { recentSearches: [], preferredGame: null, favoriteGames: [] };
  let isProcessing = false;

  // FA иконки
  const FA = {
    greeting: 'fa-solid fa-hand-sparkles', farewell: 'fa-solid fa-hand-peace', thanks: 'fa-solid fa-heart',
    about: 'fa-solid fa-robot', help: 'fa-solid fa-circle-question', pro: 'fa-solid fa-crown',
    how: 'fa-solid fa-shield-halved', safety: 'fa-solid fa-lock', seller: 'fa-solid fa-store',
    verify: 'fa-solid fa-certificate', achievement: 'fa-solid fa-trophy', refund: 'fa-solid fa-rotate-left',
    dispute: 'fa-solid fa-scale-balanced', balance: 'fa-solid fa-wallet', withdraw: 'fa-solid fa-money-bill-transfer',
    contacts: 'fa-solid fa-address-book', reviews: 'fa-solid fa-star', guide: 'fa-solid fa-book',
    compare: 'fa-solid fa-chart-simple', trending: 'fa-solid fa-fire', discounts: 'fa-solid fa-tag',
    search: 'fa-solid fa-magnifying-glass', cheap: 'fa-solid fa-piggy-bank', best: 'fa-solid fa-medal',
    stats: 'fa-solid fa-chart-pie', market: 'fa-solid fa-shop', user: 'fa-solid fa-user',
    game: 'fa-solid fa-gamepad', price: 'fa-solid fa-ruble-sign', rating: 'fa-solid fa-star',
    support: 'fa-solid fa-headset', warning: 'fa-solid fa-triangle-exclamation',
    success: 'fa-solid fa-circle-check', error: 'fa-solid fa-circle-xmark', info: 'fa-solid fa-circle-info',
    clock: 'fa-solid fa-clock', link: 'fa-solid fa-arrow-up-right-from-square',
    refresh: 'fa-solid fa-arrows-rotate', close: 'fa-solid fa-xmark', send: 'fa-solid fa-paper-plane'
  };

  const GAME_DATA = {
    'dota 2': { color: '#e74c3c', fa: 'fa-solid fa-shield-halved' },
    'cs2': { color: '#f39c12', fa: 'fa-solid fa-crosshairs' },
    'valorant': { color: '#ff4155', fa: 'fa-solid fa-bullseye' },
    'league of legends': { color: '#3498db', fa: 'fa-solid fa-wand-sparkles' },
    'world of warcraft': { color: '#2ecc71', fa: 'fa-solid fa-dragon' }
  };

  function getGameData(game) {
    const g = (game || '').toLowerCase().trim();
    for (const [key, data] of Object.entries(GAME_DATA)) { if (g.includes(key)) return data; }
    return { color: '#8b5cf6', fa: 'fa-solid fa-gamepad' };
  }

  const $ = (sel, ctx = document) => ctx.querySelector(sel);
  const $$ = (sel, ctx = document) => [...ctx.querySelectorAll(sel)];
  const formatPrice = p => Math.round(p).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ' ') + ' ₽';
  const pick = arr => arr[Math.floor(Math.random() * arr.length)];
  const now = () => new Date();
  function faIcon(icon) { return `<i class="${icon}" style="margin-right:0.4rem;"></i>`; }

  function getUserName() {
    const trigger = document.querySelector('.user-dropdown-trigger span');
    if (trigger) return trigger.textContent.trim();
    return '';
  }

  // Загрузка данных
  async function fetchMarketData() {
    const n = Date.now();
    if (marketData.length && (n - lastMarketFetch) < CONFIG.cacheMs) return marketData;
    try {
      const resp = await fetch('/marketplace');
      const html = await resp.text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const cards = doc.querySelectorAll('.mp-card, .boost-card');
      marketData = [];
      cards.forEach(card => {
        const game = card.getAttribute('data-game') || '';
        const title = card.getAttribute('data-title') || '';
        const price = parseFloat(card.getAttribute('data-price')) || 0;
        const rating = parseFloat(card.getAttribute('data-rating')) || 0;
        const reviews = parseInt(card.getAttribute('data-reviews')) || 0;
        const id = card.getAttribute('data-id') || '';
        if (title && price > 0) marketData.push({ id, game, title, price, rating, reviews, addedAt: new Date() });
      });
      lastMarketFetch = n;
    } catch(e) { /* fallback */ }
    return marketData;
  }

  async function fetchStats() {
    const n = Date.now();
    if (platformStats && (n - lastStatsFetch) < CONFIG.cacheMs * 2) return platformStats;
    try {
      const resp = await fetch('/');
      const html = await resp.text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      platformStats = {};
      doc.querySelectorAll('.stat-card, .stat-item').forEach(card => {
        const v = $(card, '.stat-value') || $(card, '.stat-val');
        const l = $(card, '.stat-label') || $(card, '.stat-lbl');
        if (v && l) platformStats[l.textContent.trim()] = v.textContent.trim();
      });
      if (!Object.keys(platformStats).length) platformStats = { 'Пользователей': '1 200+', 'Заказов': '5 800+', 'Рейтинг': '4.9/5', 'Онлайн': '60+' };
      lastStatsFetch = n;
    } catch(e) { platformStats = { 'Пользователей': '1 200+', 'Заказов': '5 800+', 'Рейтинг': '4.9/5', 'Онлайн': '60+' }; }
    return platformStats;
  }

  // Поиск
  function searchEngine(query, filters = {}) {
    let results = [...marketData];
    const q = (query || '').toLowerCase().trim();
    if (q) results = results.filter(item => item.title.toLowerCase().includes(q) || item.game.toLowerCase().includes(q));
    if (filters.game) { const g = filters.game.toLowerCase(); results = results.filter(item => item.game.toLowerCase().includes(g)); }
    if (filters.maxPrice) results = results.filter(item => item.price <= filters.maxPrice);
    if (filters.minPrice) results = results.filter(item => item.price >= filters.minPrice);
    const sortMap = { 'price_asc': (a,b) => a.price - b.price, 'price_desc': (a,b) => b.price - a.price, 'rating': (a,b) => (b.rating||0) - (a.rating||0), 'default': (a,b) => (b.rating||0) - (a.rating||0) || a.price - b.price };
    results.sort(sortMap[filters.sort] || sortMap.default);
    return results;
  }

  function getStats(items) {
    if (!items.length) return null;
    const prices = items.map(i => i.price);
    const ratings = items.filter(i => i.rating > 0).map(i => i.rating);
    return { count: items.length, min: Math.min(...prices), max: Math.max(...prices), avg: Math.round(prices.reduce((a,b) => a+b, 0) / prices.length), avgRating: ratings.length ? (ratings.reduce((a,b) => a+b, 0) / ratings.length).toFixed(1) : 0, cheapest: items.reduce((a,b) => a.price < b.price ? a : b), best: items.reduce((a,b) => (b.rating||0) > (a.rating||0) ? b : a) };
  }

  function buildCard(item, num) {
    const url = item.id ? '/booster/' + item.id : '/marketplace';
    const gd = getGameData(item.game);
    return `<a href="${url}" target="_blank" style="display:block;text-decoration:none;color:inherit;background:rgba(255,255,255,0.025);border:1px solid rgba(255,255,255,0.05);border-radius:1rem;padding:0.9rem;margin-top:0.5rem;transition:all 0.25s ease;cursor:pointer;" onmouseover="this.style.background='rgba(139,92,246,0.08)';this.style.borderColor='rgba(139,92,246,0.25)';this.style.transform='translateX(3px)';this.style.boxShadow='0 4px 18px rgba(139,92,246,0.12)'" onmouseout="this.style.background='rgba(255,255,255,0.025)';this.style.borderColor='rgba(255,255,255,0.05)';this.style.transform='none';this.style.boxShadow='none'"><div style="display:flex;align-items:center;gap:0.7rem;"><div style="width:44px;height:44px;border-radius:12px;background:linear-gradient(135deg,${gd.color},${gd.color}dd);display:flex;align-items:center;justify-content:center;flex-shrink:0;color:white;box-shadow:0 2px 8px rgba(0,0,0,0.2);"><i class="${gd.fa}"></i></div><div style="flex:1;min-width:0;"><div style="font-weight:600;font-size:0.83rem;color:#f5f5f7;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">${num ? num + '. ' : ''}${item.title}</div><div style="display:flex;align-items:center;gap:0.5rem;font-size:0.7rem;color:rgba(255,255,255,0.35);margin-top:0.15rem;"><span><i class="${gd.fa}" style="font-size:0.6rem;"></i> ${item.game||'—'}</span>${item.rating ? `<span><i class="fa-solid fa-star" style="color:#fbbf24;font-size:0.6rem;"></i> ${item.rating.toFixed(1)}</span>` : ''}</div></div><div style="text-align:right;flex-shrink:0;"><div style="font-weight:700;font-size:0.9rem;color:#a78bfa;">${formatPrice(item.price)}</div></div></div></a>`;
  }

  function cardsList(items, start = 1) { 
    const limited = items.slice(0, 3);
    return limited.map((item, i) => buildCard(item, start + i)).join(''); 
  }

  function allItemsLink(count, game) {
    const params = game ? '?game=' + encodeURIComponent(game) : '';
    const text = count > 3 
      ? `Смотреть все ${count} предложений в Маркетплейсе` 
      : 'Перейти в Маркетплейс';
    return `<a href="/marketplace${params}" style="display:block;text-align:center;margin-top:0.6rem;padding:0.5rem;border-radius:0.7rem;background:rgba(139,92,246,0.1);color:#a78bfa;text-decoration:none;font-size:0.8rem;font-weight:600;transition:all 0.2s;" onmouseover="this.style.background='rgba(139,92,246,0.2)'" onmouseout="this.style.background='rgba(139,92,246,0.1)'"><i class="fa-solid fa-arrow-right"></i> ${text}</a>`;
  }
  // Анализ текста
  function extractGame(text) {
    const t = text.toLowerCase();
    if (/dota\s*2|дот[ауе]/i.test(t)) return 'Dota 2';
    if (/cs\s*[2g]|кс\s*[2g]|ксго|контр|counter.?strike/i.test(t)) return 'CS2';
    if (/valorant|валорант|валер/i.test(t)) return 'Valorant';
    if (/league\s*of\s*legends|lol|лига\s*легенд|лол/i.test(t)) return 'League of Legends';
    if (/world\s*of\s*warcraft|wow|варкрафт|вов/i.test(t)) return 'World of Warcraft';
    return null;
  }

  function extractPriceRange(text) {
    const max = text.match(/до\s*(\d{2,})\s*(?:₽|руб|р\.)?/) || [null, null];
    const min = text.match(/от\s*(\d{2,})\s*(?:₽|руб|р\.)?/) || [null, null];
    return { maxPrice: max[1] ? parseInt(max[1]) : 0, minPrice: min[1] ? parseInt(min[1]) : 0 };
  }

  function cleanQuery(text) {
    return text.replace(/покажи|найди|поищи|предлож(и|ения|ений)|ищу|нужен|нужна|нужно|хочу|подбери|посоветуй|что\s*есть|какие\s*есть|сколько\s*стоит|цена|цены|сравни|анализ|статистик|товар|услуга/gi, '').replace(/буст[ыа]?|скин[ыа]?|аккаунт[ыа]?|калибровка[аи]?/gi, '').replace(/дешёв[ыае]+|подешевле|эконом|бюджетн[ыае]+|дёшево/gi, '').replace(/лучш[ие]+|топ[овые]*|сам[ыое]+|проверен[ные]*|надёжн[ые]*|премиум|элитн[ые]*/gi, '').replace(/от\s*\d+|до\s*\d+/gi, '').replace(/\s+/g, ' ').trim();
  }

  function detectIntent(text) {
    const t = text.toLowerCase().trim();
    if (/^(привет|здравствуй|hi|hello|ку|хай|салют|добрый\s(день|вечер|утро)|приветствую|здарова|хеллоу)/i.test(t)) return 'greeting';
    if (/^(пока|прощай|bye|до\sсвидания|увидимся|счастливо|спокойной\sночи|доброй\sночи|чао)\s*$/i.test(t)) return 'farewell';
    if (/^(спасибо|спс|thanks|thx|благодарю|сенкс|пасиб)\s*$/i.test(t)) return 'thanks';
    if (/(кто\sты|что\sты|ты\sкто|тво[ёе]\sимя|как\sтебя\sзовут|ты\sчеловек|ты\sробот|ты\sии|ты\sai)/i.test(t)) return 'about_bot';
    if (/(что\s(ты\s)?умеешь|помощь|help|возможности|функции|команды|справка)/i.test(t)) return 'help';
    if (/(стать\sпродавц|начать\sпродавать|как\sпродавать|хочу\sпродавать|создать\sпредложение|добавить\sтовар)/i.test(t)) return 'become_seller';
    if (/(pro|подписк|премиум|premium|комисс|пробный|продлить)/i.test(t)) return 'pro';
    if (/(как\sработает|схема|принцип|процесс|объясни|расскажи\sкак|в\sчём\sсуть|что\sэто\sза\sсайт)/i.test(t)) return 'how';
    if (/(безопасн|эскроу|escrow|защит|гарант|надёж|кинут|обман|мошен|можно\sдоверять)/i.test(t)) return 'safety';
    if (/(верификац|verify|проверк|галочка|подтвержден|паспорт)/i.test(t)) return 'verification';
    if (/(достижен|achievement|бейдж|медаль|трофей|наград|ачивк)/i.test(t)) return 'achievements';
    if (/(возврат|вернуть|refund|отмена|отменить|деньги\sназад)/i.test(t)) return 'refund';
    if (/(спор|диспут|dispute|проблем|конфликт|не\sвыполн|не\sсделал|жалоб)/i.test(t)) return 'dispute';
    if (/(баланс|пополнит|кошелёк|wallet|зачислить|платёж|пополнить\sсчёт)/i.test(t)) return 'balance';
    if (/(вывод|вывести|снять|обналичить|withdraw|заработан|получить\sденьги)/i.test(t)) return 'withdraw';
    if (/(контакт|поддержк|support|связь|написать|почта|email|дискорд|телеграм|tg\b)/i.test(t)) return 'contacts';
    if (/(отзыв|review|оценить|рейтинг\sпродавц|как\sоставить\sотзыв)/i.test(t)) return 'reviews';
    if (/(как\sкупить|как\sзаказать|инструкция|гайд|как\sпользоваться|с\sчего\sначать)/i.test(t)) return 'guide';
    if (/(сравн|что\sлучше|что\sвыбрать|какой\sлучше|посоветуй\sигру)/i.test(t)) return 'compare_games';
    if (/(популярн|тренд|часто\sпокупают|в\sтопе|хит\sпродаж)/i.test(t)) return 'trending';
    if (/(скидк|акци[яи]|промокод|coupon|дешевл|распродаж)/i.test(t)) return 'discounts';
    if (/покажи|найди|поищи|предлож|ищу|нужен|нужна|нужно|хочу|подбери|посоветуй|скин|буст|аккаунт|калибровка|товар|услуга|цена|стоимость|сколько/i.test(t)) return 'search';
    if (extractGame(t)) return 'search';
    return 'general';
  }

  // Главный интеллект
  async function generateResponse(text) {
    await fetchMarketData();
    await fetchStats();

    conversationHistory.push({ role: 'user', text, time: now() });
    if (conversationHistory.length > CONFIG.conversationMemory) conversationHistory.shift();
    if (userPreferences.recentSearches.length > CONFIG.maxRecentSearches) userPreferences.recentSearches.shift();

    const intent = detectIntent(text);
    const game = extractGame(text);
    const { maxPrice, minPrice } = extractPriceRange(text);
    const t = text.toLowerCase();
    if (game) userPreferences.preferredGame = game;
    const userName = getUserName();

    // Приветствие
    if (intent === 'greeting') {
      const hour = now().getHours();
      const timeGreet = hour < 6 ? 'Доброй ночи' : hour < 12 ? 'Доброе утро' : hour < 18 ? 'Добрый день' : 'Добрый вечер';
      const namePart = userName ? `, <b>${userName}</b>` : '';
      const statsStr = platformStats ? Object.entries(platformStats).map(([k,v]) => `${k}: ${v}`).join(' • ') : '';
      const recent = userPreferences.recentSearches.length ? '\n\n<i class="fa-solid fa-clock-rotate-left" style="color:rgba(255,255,255,0.3);margin-right:0.3rem;"></i>Недавно искали: ' + userPreferences.recentSearches.slice(-3).join(', ') : '';
      return `${faIcon(FA.greeting)}${timeGreet}${namePart}! Я AI-помощник XSoneBMP.\n\n${statsStr ? faIcon(FA.stats) + statsStr + '\n\n' : ''}Могу найти товары и услуги, сравнить цены, рассказать о платформе. Спросите: «покажи предложения Dota 2» или «расскажи про PRO».${recent}`;
    }

    if (intent === 'farewell') return faIcon(FA.farewell) + pick(['До встречи!', 'Пока! Заходите.', 'Удачи!', 'Хорошего дня!', 'Чао!']);
    if (intent === 'thanks') return faIcon(FA.thanks) + pick(['Рад помочь!', 'Всегда пожалуйста!', 'Обращайтесь!', 'Приятно было помочь!']);
    if (intent === 'about_bot') return `${faIcon(FA.about)}<b>Обо мне:</b>\n\nЯ XSone AI — виртуальный помощник маркетплейса XSoneBMP.\n\n${faIcon(FA.search)}Ищу товары по реальным ценам\n${faIcon(FA.compare)}Сравниваю предложения\n${faIcon(FA.info)}Отвечаю на вопросы\n\nСейчас в базе: <b>${marketData.length}</b> активных предложений.`;
    if (intent === 'help') return `${faIcon(FA.help)}<b>Что я умею:</b>\n\n${faIcon(FA.search)}<b>Поиск:</b>\n• «покажи предложения Dota 2»\n• «найди буст в CS2 до 2000₽»\n• «покажи дешёвые скины»\n• «лучшие предложения Valorant»\n\n${faIcon(FA.stats)}<b>Аналитика:</b>\n• «что популярно?»\n• «сравни игры»\n• «какие есть скидки?»\n\n${faIcon(FA.info)}<b>Информация:</b>\n• «расскажи про PRO»\n• «как работает?»\n• «безопасность»\n• «как стать продавцом?»\n• «контакты»`;

    if (intent === 'pro') return { text: '', html: `<div style="background:linear-gradient(135deg,rgba(245,158,11,0.1),rgba(245,158,11,0.02));border:1px solid rgba(245,158,11,0.2);border-radius:1rem;padding:1rem;"><div style="font-weight:700;font-size:0.9rem;color:#fbbf24;margin-bottom:0.6rem;"><i class="fa-solid fa-crown"></i> XSoneBMP PRO</div><div style="font-size:0.78rem;color:rgba(255,255,255,0.7);line-height:1.6;"><i class="fa-solid fa-check" style="color:#10b981;"></i> Комиссия <b>2%</b> вместо 5%<br><i class="fa-solid fa-chart-line" style="color:#60a5fa;"></i> Аналитика рынка<br><i class="fa-solid fa-arrow-up" style="color:#fbbf24;"></i> Приоритет в поиске<br><i class="fa-solid fa-message" style="color:#a78bfa;"></i> Автосообщения<br><i class="fa-solid fa-palette" style="color:#c084fc;"></i> Кастомизация профиля<br><i class="fa-solid fa-gift" style="color:#fbbf24;"></i> <b>7 дней бесплатно</b></div><a href="/upgrade-pro" style="display:block;text-align:center;margin-top:0.7rem;padding:0.5rem;border-radius:0.6rem;background:linear-gradient(135deg,#f59e0b,#d97706);color:#000;text-decoration:none;font-size:0.8rem;font-weight:700;"><i class="fa-solid fa-rocket"></i> Подробнее</a></div>` };
    if (intent === 'how') return `${faIcon(FA.how)}<b>Как работает XSoneBMP:</b>\n\n<b>Покупатель:</b>\n1️⃣ Выбираете товар\n2️⃣ Оплачиваете — деньги в эскроу\n3️⃣ Продавец выполняет\n4️⃣ Подтверждаете\n\n<b>Продавец:</b>\n1️⃣ Создаёте предложение\n2️⃣ Получаете заказ\n3️⃣ Выполняете\n4️⃣ Получаете оплату`;
    if (intent === 'safety') return `${faIcon(FA.safety)}<b>Безопасность:</b>\n\n• Эскроу — деньги заморожены\n• Верификация продавцов\n• Рейтинг и отзывы\n• Система споров\n• Защита данных`;
    if (intent === 'become_seller') return `${faIcon(FA.seller)}<b>Как начать продавать:</b>\n\n1️⃣ Профиль → «Добавить предложение»\n2️⃣ Заполните описание и цену\n3️⃣ Отправьте\n\n💡 Пройдите верификацию для доверия. PRO снижает комиссию до 2%.`;
    if (intent === 'verification') return `${faIcon(FA.verify)}<b>Верификация:</b> бесплатная проверка. Заявка до 24 часов. Даёт значок ✅ и доверие покупателей.`;
    if (intent === 'achievements') return `${faIcon(FA.achievement)}<b>Достижения:</b> 🌟 Опытный (10 заказов) • 👑 Мастер (50) • ⭐ Топ-рейтинг (4.8+) • 💎 PRO • ✅ Верифицирован • 💰 Заработок 100K+`;
    if (intent === 'refund') return `${faIcon(FA.refund)}<b>Возврат:</b> откройте спор → опишите проблему → администратор вернёт деньги.`;
    if (intent === 'dispute') return `${faIcon(FA.dispute)}<b>Споры:</b> откройте спор в заказе, приложите скриншоты. Решение до 24 часов.`;
    if (intent === 'balance') return `${faIcon(FA.balance)}<b>Пополнение:</b> СБП (мгновенно), карта, криптовалюта. Минимум 100 ₽.`;
    if (intent === 'withdraw') return `${faIcon(FA.withdraw)}<b>Вывод:</b> на карту (1-3 дня), СБП (мгновенно), крипта (10-30 мин). Минимум 500 ₽.`;
    if (intent === 'contacts') return `${faIcon(FA.contacts)}<b>Контакты:</b> support@xsonebmp.com • Discord: discord.gg/xsonebmp • Telegram: @xsonebmp • /support`;
    if (intent === 'reviews') return `${faIcon(FA.reviews)}<b>Отзывы:</b> после заказа оцените продавца (1-5 ⭐). Отзывы формируют рейтинг.`;
    if (intent === 'guide') return `${faIcon(FA.guide)}<b>Как купить:</b> Маркетплейс → выбрать игру → найти товар → корзина → оплатить → подтвердить.`;

    if (intent === 'compare_games') {
      if (!marketData.length) return 'Недостаточно данных.';
      const games = {};
      marketData.forEach(item => { if (!games[item.game]) games[item.game] = []; games[item.game].push(item); });
      let html = `<div style="font-size:0.85rem;color:rgba(255,255,255,0.8);margin-bottom:0.7rem;"><i class="fa-solid fa-chart-simple"></i> <b>Сравнение игр:</b></div><div style="display:flex;flex-direction:column;gap:0.5rem;">`;
      for (const [g, items] of Object.entries(games)) {
        const gd = getGameData(g); const prices = items.map(i => i.price);
        const avgR = items.filter(i => i.rating > 0).reduce((a,b) => a + b.rating, 0) / items.filter(i => i.rating > 0).length || 0;
        html += `<div style="background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.06);border-radius:0.8rem;padding:0.7rem;display:flex;align-items:center;gap:0.8rem;"><div style="font-size:1.5rem;"><i class="${gd.fa}" style="color:${gd.color};"></i></div><div style="flex:1;"><div style="font-weight:600;font-size:0.8rem;">${g}</div><div style="font-size:0.7rem;color:rgba(255,255,255,0.4);">${items.length} предл. • ${formatPrice(Math.min(...prices))} — ${formatPrice(Math.max(...prices))} • ${avgR.toFixed(1)} ⭐</div></div></div>`;
      }
      html += '</div>';
      return { text: '', html };
    }

    if (intent === 'trending') {
      if (!marketData.length) return 'Недостаточно данных.';
      const byGame = {};
      marketData.forEach(item => { byGame[item.game] = (byGame[item.game]||0) + 1; });
      const sorted = Object.entries(byGame).sort((a,b) => b[1] - a[1]);
      const topRated = [...marketData].sort((a,b) => (b.rating||0) - (a.rating||0)).slice(0, 3);
      let text = `${faIcon(FA.trending)}<b>Популярное:</b>\n\n`;
      sorted.forEach(([g, cnt], i) => { const gd = getGameData(g); text += `${i+1}. <i class="${gd.fa}"></i> ${g}: ${cnt} предл.\n`; });
      return { text, html: cardsList(topRated) };
    }

    if (intent === 'discounts') return `${faIcon(FA.discounts)}<b>Скидки и акции:</b>\n\n• Первый заказ: до 15% (промокод FIRST)\n• PRO: 7 дней бесплатно\n• Сезонные акции\n• Промокоды при оформлении`;

    // Поиск
    if (intent === 'search' || game || maxPrice > 0 || minPrice > 0) {
      let query = cleanQuery(text);
      if (query && !userPreferences.recentSearches.includes(query)) userPreferences.recentSearches.push(query);
      if (game && !userPreferences.recentSearches.includes(game)) userPreferences.recentSearches.push(game);

      const isCheapest = /(дешев|недорого|бюджет|подешевле|эконом|минимальн|дёшево)/i.test(t);
      const isBest = /(лучш|топ|самый|рейтинг|проверен|надёжн|премиум|элитн)/i.test(t);
      const isCompare = /(сравн|анализ|статистик|разниц|диапазон|разброс|обзор)/i.test(t);

      const filters = { game, maxPrice: maxPrice||undefined, minPrice: minPrice||undefined, sort: isBest?'rating':isCheapest?'price_asc':'default' };
      const items = searchEngine(query, filters);

      if (!items.length) {
        let msg = `${faIcon(FA.warning)}<b>Ничего не найдено</b>`;
        if (game) msg += ' для ' + game;
        if (query) msg += ' по запросу «' + query + '»';
        if (maxPrice) msg += ' до ' + formatPrice(maxPrice);
        msg += '.\n\n💡 <b>Попробуйте:</b>\n• Другую игру: Dota 2, CS2, Valorant, LoL, WoW\n• Изменить бюджет';
        if (marketData.length) {
          const games = {};
          marketData.forEach(i => { games[i.game] = (games[i.game]||0) + 1; });
          msg += '\n\n📊 <b>Доступно на платформе:</b>';
          for (const [g, cnt] of Object.entries(games)) { const gd = getGameData(g); msg += `\n• <i class="${gd.fa}"></i> ${g}: ${cnt} предл.`; }
        }
        return msg;
      }

      const stats = getStats(items);
      if (maxPrice > 0 && !isCheapest && !isBest && !isCompare) {
        const topItems = items.slice(0, CONFIG.maxCards);
        return { text: `${faIcon(FA.search)}<b>Найдено ${stats.count} предложений</b> до ${formatPrice(maxPrice)}${game?' в '+game:''}\nЦены: ${formatPrice(stats.min)} — ${formatPrice(stats.max)}${stats.avgRating?' • '+stats.avgRating+' ⭐':''}`, html: cardsList(topItems) + allItemsLink(items.length) };
      }
      if (isCheapest && stats) return { text: `${faIcon(FA.cheap)}<b>Самое доступное</b> (${stats.count}):`, html: buildCard(stats.cheapest) + `<div style="margin-top:0.4rem;font-size:0.7rem;color:rgba(255,255,255,0.3);">Диапазон: ${formatPrice(stats.min)} — ${formatPrice(stats.max)}</div>` + allItemsLink(stats.count) };
      if (isBest && stats) return { text: `${faIcon(FA.best)}<b>Лучшее по рейтингу</b> (${stats.best.rating?.toFixed(1)} ⭐):`, html: buildCard(stats.best) + allItemsLink(stats.count) };
      if (isCompare && stats) return { text: '', html: `<div style="font-size:0.85rem;color:rgba(255,255,255,0.8);margin-bottom:0.7rem;"><i class="fa-solid fa-chart-pie"></i> <b>Анализ рынка</b>${game?' — '+game:''}</div><div style="display:grid;grid-template-columns:1fr 1fr;gap:0.5rem;font-size:0.76rem;margin-bottom:0.8rem;"><div style="background:rgba(255,255,255,0.03);padding:0.6rem;border-radius:0.6rem;text-align:center;"><div style="color:rgba(255,255,255,0.3);">Предложений</div><div style="font-weight:700;font-size:1.1rem;">${stats.count}</div></div><div style="background:rgba(255,255,255,0.03);padding:0.6rem;border-radius:0.6rem;text-align:center;"><div style="color:rgba(255,255,255,0.3);">Средняя</div><div style="font-weight:700;font-size:1.1rem;color:#a78bfa;">${formatPrice(stats.avg)}</div></div><div style="background:rgba(255,255,255,0.03);padding:0.6rem;border-radius:0.6rem;text-align:center;"><div style="color:rgba(255,255,255,0.3);">Мин.</div><div style="font-weight:700;color:#10b981;">${formatPrice(stats.min)}</div></div><div style="background:rgba(255,255,255,0.03);padding:0.6rem;border-radius:0.6rem;text-align:center;"><div style="color:rgba(255,255,255,0.3);">Макс.</div><div style="font-weight:700;color:#ef4444;">${formatPrice(stats.max)}</div></div></div><div style="font-size:0.72rem;color:rgba(255,255,255,0.35);text-align:center;">💡 Оптимально: ${formatPrice(stats.avg*0.85)} — ${formatPrice(stats.avg*1.15)}</div>` };
      const topItems = items.slice(0, CONFIG.maxCards);
      return { text: `${faIcon(FA.search)}<b>Найдено ${stats.count} предложений</b>${game?' в '+game:''}\n${formatPrice(stats.min)} — ${formatPrice(stats.max)}${stats.avgRating?' • '+stats.avgRating+' ⭐':''}`, html: cardsList(topItems) + allItemsLink(items.length) };
    }

    // Угадываем
    if (marketData.length && text.length > 1) {
      const guessed = searchEngine(cleanQuery(text), { maxPrice: maxPrice||undefined, minPrice: minPrice||undefined });
      if (guessed.length > 0) return { text: `${faIcon(FA.info)}<b>Возможно вы ищете</b>${maxPrice?' до '+formatPrice(maxPrice):''}:`, html: cardsList(guessed.slice(0, 3)) + `<a href="/marketplace" style="display:block;text-align:center;margin-top:0.5rem;padding:0.5rem;border-radius:0.7rem;background:rgba(139,92,246,0.1);color:#a78bfa;text-decoration:none;font-size:0.8rem;font-weight:600;"><i class="fa-solid fa-shop"></i> Открыть Маркетплейс</a>` };
    }

    return `${faIcon(FA.info)}Я не совсем понял. Попробуйте:\n\n• «покажи предложения Dota 2»\n• «найди буст в CS2 до 2000₽»\n• «расскажи про PRO»\n• «что популярно?»\n• «что ты умеешь?»`;
  }

  // UI
  function init() {
    fetchMarketData(); fetchStats();

    const fab = document.createElement('button');
    fab.id = 'aiFab'; fab.className = 'ai-fab'; fab.title = 'AI-помощник';
    fab.innerHTML = '<i class="fa-solid fa-robot"></i>';
    fab.addEventListener('click', toggleChat);
    document.body.appendChild(fab);

    const userName = getUserName();
    const greetName = userName ? `<b>Привет, ${userName}!</b>` : '<b>Привет!</b>';

    const chat = document.createElement('div');
    chat.id = 'aiChat'; chat.className = 'ai-chat';
    chat.innerHTML = `
      <div class="ai-header">
        <div class="ai-avatar"><i class="fa-solid fa-robot"></i></div>
        <div class="ai-header-info"><div class="ai-header-name">XSone Helper</div><div class="ai-header-status" id="aiStatus">Готов помочь</div></div>
        <div style="display:flex;gap:0.3rem;">
          <button class="ai-icon-btn" id="aiRefresh" title="Обновить"><i class="fa-solid fa-arrows-rotate"></i></button>
          <button class="ai-icon-btn" id="aiClose" title="Закрыть"><i class="fa-solid fa-xmark"></i></button>
        </div>
      </div>
      <div class="ai-messages" id="aiMessages">
        <div class="ai-msg bot"><i class="fa-solid fa-hand-sparkles" style="color:#a78bfa;margin-right:0.4rem;"></i> ${greetName} Я Помощник XSoneBMP.<br><br>Могу найти товары, сравнить цены, рассказать о платформе.<br><br>Спросите: «покажи предложения Dota 2» или «что ты умеешь?»</div>
      </div>
      <div class="ai-hints">
        <span class="ai-hint" data-hint="Покажи предложения Dota 2"><i class="fa-solid fa-shield-halved"></i> Dota 2</span>
        <span class="ai-hint" data-hint="Покажи предложения CS2"><i class="fa-solid fa-crosshairs"></i> CS2</span>
        <span class="ai-hint" data-hint="Что популярно?"><i class="fa-solid fa-fire"></i> Популярное</span>
        <span class="ai-hint" data-hint="Расскажи про PRO подписку"><i class="fa-solid fa-crown"></i> PRO</span>
        <span class="ai-hint" data-hint="Как работает платформа?"><i class="fa-solid fa-shield-halved"></i> Как работает</span>
        <span class="ai-hint" data-hint="Сравни цены"><i class="fa-solid fa-chart-simple"></i> Сравнить</span>
      </div>
      <div class="ai-input-wrap">
        <input type="text" class="ai-input" id="aiInput" placeholder="Спросите что-нибудь..." autocomplete="off">
        <button class="ai-send" id="aiSend" title="Отправить"><i class="fa-solid fa-paper-plane"></i></button>
      </div>
    `;
    document.body.appendChild(chat);

    $('#aiClose').addEventListener('click', toggleChat);
    $('#aiRefresh').addEventListener('click', async () => {
      lastMarketFetch = 0; await fetchMarketData();
      addMsg('<i class="fa-solid fa-check-circle" style="color:#10b981;"></i> Данные обновлены. ' + marketData.length + ' товаров.', 'bot');
    });
    $('#aiSend').addEventListener('click', sendMessage);
    $('#aiInput').addEventListener('keydown', e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendMessage(); } });
    chat.querySelectorAll('.ai-hint').forEach(h => h.addEventListener('click', () => { $('#aiInput').value = h.dataset.hint; sendMessage(); }));
    document.addEventListener('keydown', e => { if (e.key === 'Escape' && chat.classList.contains('open')) toggleChat(); });
  }

  function toggleChat() {
    const c = document.getElementById('aiChat'), f = document.getElementById('aiFab');
    if (c.classList.contains('open')) {
      c.style.animation = 'aiSlideDown 0.3s cubic-bezier(0.34, 1.56, 0.64, 1) forwards';
      setTimeout(() => { c.classList.remove('open'); c.style.animation = ''; f.style.display = 'flex'; }, 280);
    } else {
      c.classList.add('open'); f.style.display = 'none';
      setTimeout(() => $('#aiInput')?.focus(), 300);
    }
  }

  async function sendMessage() {
    const input = $('#aiInput'), text = input.value.trim();
    if (!text || isProcessing) return;
    isProcessing = true;
    addMsg(text, 'user'); input.value = ''; input.focus(); showTyping();
    try {
      const r = await generateResponse(text);
      const delay = CONFIG.typingDelayMin + Math.random() * (CONFIG.typingDelayMax - CONFIG.typingDelayMin);
      setTimeout(() => { hideTyping(); if (typeof r === 'object') addMsgHtml(r.text||'', r.html||'', 'bot'); else addMsg(r, 'bot'); isProcessing = false; }, Math.min(delay, 300 + text.length * 15));
    } catch(e) { hideTyping(); addMsg('<i class="fa-solid fa-circle-xmark" style="color:#ef4444;"></i> Ошибка.', 'bot'); isProcessing = false; }
  }

  function addMsg(text, type) {
    const el = document.createElement('div'); el.className = 'ai-msg ' + type;
    el.innerHTML = text.replace(/\n/g, '<br>');
    const c = $('#aiMessages'); c.appendChild(el); c.scrollTop = c.scrollHeight;
  }

  function addMsgHtml(text, html, type) {
    const el = document.createElement('div'); el.className = 'ai-msg ' + type;
    if (text) { const p = document.createElement('div'); p.innerHTML = text.replace(/\n/g, '<br>'); p.style.marginBottom = html?'0.5rem':'0'; el.appendChild(p); }
    if (html) { const d = document.createElement('div'); d.innerHTML = html; el.appendChild(d); }
    const c = $('#aiMessages'); c.appendChild(el); c.scrollTop = c.scrollHeight;
  }

  function showTyping() {
    const el = document.createElement('div'); el.className = 'ai-msg bot typing'; el.id = 'aiTyping';
    el.innerHTML = '<div class="ai-dots"><span></span><span></span><span></span></div>';
    $('#aiMessages').appendChild(el); $('#aiMessages').scrollTop = $('#aiMessages').scrollHeight;
  }

  function hideTyping() { document.getElementById('aiTyping')?.remove(); }

  const style = document.createElement('style');
  style.textContent = `.ai-icon-btn{background:none;border:none;color:rgba(255,255,255,0.4);cursor:pointer;font-size:0.9rem;padding:0.3rem 0.4rem;border-radius:0.5rem;transition:all 0.2s;display:flex;align-items:center;justify-content:center;}.ai-icon-btn:hover{color:#fff;background:rgba(255,255,255,0.06);}@keyframes pulse{0%,100%{opacity:1}50%{opacity:0.4}}@keyframes aiSlideDown{from{transform:translateY(0) scale(1);opacity:1}to{transform:translateY(20px) scale(0.9);opacity:0}}`;
  document.head.appendChild(style);

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();

  window.XSoneAI = { open: toggleChat, send: sendMessage, getMarketData: () => marketData, refresh: fetchMarketData, getStats: () => platformStats, getHistory: () => conversationHistory };
  window.toggleAiChat = toggleChat;
  window.sendAiMessage = sendMessage;
})();