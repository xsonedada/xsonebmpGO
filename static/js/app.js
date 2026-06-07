// static/js/app.js

// ---------- Утилиты ----------
const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => document.querySelectorAll(sel);

function escapeHTML(str) {
  if (!str) return '';
  const div = document.createElement('div');
  div.appendChild(document.createTextNode(str));
  return div.innerHTML;
}

function getCSRFToken() {
  return document.querySelector('meta[name="csrf-token"]')?.content || '';
}

function csrfHeaders(extra = {}) {
  const headers = { ...extra };
  const token = getCSRFToken();
  if (token) headers['X-CSRF-Token'] = token;
  return headers;
}

function timeAgo(date) {
  const diff = Math.floor((Date.now() - new Date(date).getTime()) / 1000);
  if (diff < 60) return 'только что';
  if (diff < 3600) return Math.floor(diff / 60) + ' мин. назад';
  if (diff < 86400) return Math.floor(diff / 3600) + ' ч. назад';
  return new Date(date).toLocaleDateString('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
}

function vibrateSoft() {
  if (navigator.vibrate) navigator.vibrate(15);
}

function playNotifySound(type) {
  if (type === 'success') AudioEngine?.success();
  else if (type === 'error') AudioEngine?.error();
  else if (type === 'warning') AudioEngine?.warning();
  else if (type === 'achievement') AudioEngine?.achievement();
  vibrateSoft();
}

function isLoggedIn() {
  return !!$('.user-dropdown');
}

// ---------- Уведомления ----------
function showNotification(message, type = 'info') {
  const container = $('#notifications');
  if (!container) return;

  // Проверка на дубликат (по тексту)
  const existing = [...container.querySelectorAll('.notif-message')].find(el => el.textContent === message);
  if (existing) {
    const parent = existing.closest('.notification');
    parent.classList.remove('shake');
    void parent.offsetWidth;
    parent.classList.add('shake');
    clearTimeout(parent._timeout);
    parent._timeout = setTimeout(() => {
      parent.classList.add('removing');
      setTimeout(() => parent.remove(), 350);
    }, 2500);
    playNotifySound(type);
    return;
  }

  const icons = {
    success: { icon: 'fa-solid fa-circle-check', title: 'Успешно' },
    error:   { icon: 'fa-solid fa-circle-xmark', title: 'Ошибка' },
    warning: { icon: 'fa-solid fa-triangle-exclamation', title: 'Внимание' },
    info:    { icon: 'fa-solid fa-circle-info', title: 'Информация' }
  };
  const { icon, title } = icons[type] || icons.info;

  const el = document.createElement('div');
  el.className = 'notification ' + type;
  el.innerHTML = `
    <div class="notif-icon-wrap"><i class="${icon}"></i></div>
    <div class="notif-content-wrap">
      <div class="notif-title">${escapeHTML(title)}</div>
      <div class="notif-message">${escapeHTML(message)}</div>
    </div>`;
  container.appendChild(el);

  playNotifySound(type);

  el._timeout = setTimeout(() => {
    el.classList.add('removing');
    setTimeout(() => el.remove(), 350);
  }, 2500);
}

// ---------- Дропдауны и меню ----------
function toggleDropdown() {
  const dropdown = $('#userDropdown');
  if (!dropdown) return;
  const isActive = dropdown.classList.toggle('active');
  document.body.style.overflow = isActive ? 'hidden' : '';
}

function toggleNotifications() {
  const dropdown = $('#notifDropdown');
  if (!dropdown) return;
  dropdown.style.display = dropdown.style.display === 'block' ? 'none' : 'block';
  if (dropdown.style.display === 'block') loadNotifications();
}

function toggleMobileMenu() {
  const navLinks = $('.nav-links');
  if (navLinks) navLinks.classList.toggle('show');
}

// Закрытие дропдаунов по клику вне
document.addEventListener('click', (e) => {
  const userDropdown = $('#userDropdown');
  if (userDropdown?.classList.contains('active') && !userDropdown.contains(e.target)) {
    userDropdown.classList.remove('active');
    document.body.style.overflow = '';
  }
  const notifDropdown = $('#notifDropdown');
  const bell = $('.notification-bell');
  if (notifDropdown && bell && !notifDropdown.contains(e.target) && !bell.contains(e.target)) {
    notifDropdown.style.display = 'none';
  }
});

// ---------- Тема ----------
function toggleTheme() {
  const html = document.documentElement;
  const newTheme = html.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
  html.setAttribute('data-theme', newTheme);
  try { localStorage.setItem('theme', newTheme); } catch (e) {}
  updateThemeIcon(newTheme);
}

function updateThemeIcon(theme) {
  const icon = $('#themeIcon');
  if (icon) icon.className = theme === 'light' ? 'fa-solid fa-sun' : 'fa-solid fa-moon';
}

function initTheme() {
  let saved;
  try { saved = localStorage.getItem('theme'); } catch (e) {}
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  const theme = saved || (prefersDark ? 'dark' : 'light');
  document.documentElement.setAttribute('data-theme', theme);
  updateThemeIcon(theme);
}

// ---------- Корзина ----------
async function loadCartCount() {
  try {
    const res = await fetch('/api/cart');
    const data = await res.json();
    const badge = $('#cartCount');
    if (!badge) return;
    if (data.items?.length > 0) {
      badge.textContent = data.items.length;
      badge.style.display = 'flex';
    } else {
      badge.style.display = 'none';
    }
  } catch (e) {}
}

async function addToCart(boostId, title, price, game, boosterId) {
  if (!isLoggedIn()) return showNotification('Войдите в аккаунт!', 'error');
  try {
    const res = await fetch('/api/cart/add', {
      method: 'POST',
      headers: csrfHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ boost_id: +boostId, title, price: +price, game, booster_id: +boosterId || 0 })
    });
    const data = await res.json().catch(() => ({}));
    const msg = data.message || data.error || (res.ok ? 'Товар добавлен' : 'Ошибка при добавлении в корзину');
    if (!res.ok && res.status === 403 && !data.message && !data.error) {
      showNotification('Обновите страницу и попробуйте снова', 'error');
      return;
    }
    showNotification(msg, data.success ? (msg.includes('уже в корзине') ? 'warning' : 'success') : 'error');
    if (data.cartCount != null) updateCartBadge(data.cartCount);
  } catch (e) {
    showNotification('Ошибка сервера', 'error');
  }
}

function updateCartBadge(count) {
  const badge = $('#cartCount');
  if (!badge) return;
  if (count > 0) {
    badge.textContent = count;
    badge.style.display = 'flex';
  } else {
    badge.style.display = 'none';
  }
}

async function removeFromCart(id) {
  await fetch('/api/cart/remove/' + id, { method: 'DELETE', headers: csrfHeaders() });
  if (window.location.pathname === '/cart') loadCartPage();
  loadCartCount();
  loadMiniCart();
}

async function clearCart() {
  if (!confirm('Очистить корзину?')) return;
  await fetch('/api/cart/clear', { method: 'DELETE', headers: csrfHeaders() });
  location.reload();
}

async function checkout() {
  try {
    const res = await fetch('/api/cart/checkout', { method: 'POST', headers: csrfHeaders() });
    const data = await res.json();
    showNotification(data.message, data.success ? 'success' : 'error');
    if (data.success) {
      updateCartBadge(0);
      const orderID = parseInt(data.orderID, 10);
      if (orderID && Number.isInteger(orderID)) {
        window.location.href = '/order/' + orderID;
      }
    }
  } catch (e) {
    showNotification('Ошибка', 'error');
  }
}

async function deleteBoost(id) {
  if (!confirm('Удалить предложение?')) return;
  await fetch('/api/boost/' + id + '/delete', { method: 'DELETE', headers: csrfHeaders() });
  location.reload();
}

// Мини-корзина
let miniCartTimer;
function showMiniCart() {
  clearTimeout(miniCartTimer);
  loadMiniCart();
  $('#miniCart').style.display = 'block';
}
function startHideTimer() { miniCartTimer = setTimeout(() => { $('#miniCart').style.display = 'none'; }, 300); }
function cancelHideTimer() { clearTimeout(miniCartTimer); }

async function loadMiniCart() {
  try {
    const res = await fetch('/api/cart');
    const data = await res.json();
    const content = $('#miniCartContent'), total = $('#miniCartTotal');
    if (!content || !total) return;
    if (data.items?.length > 0) {
      let html = '';
      data.items.slice(0, 5).forEach(item => {
        html += `<div style="display:flex;gap:0.6rem;padding:0.7rem 0;border-bottom:1px solid rgba(255,255,255,0.05);align-items:center;">
          <div style="width:36px;height:36px;background:rgba(139,92,246,0.15);border-radius:10px;display:flex;align-items:center;justify-content:center;">
            <i class="fa-solid fa-gamepad" style="color:#a78bfa;font-size:0.85rem;"></i>
          </div>
          <div style="flex:1;min-width:0;">
            <p style="margin:0;font-size:0.82rem;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">${escapeHTML(item.title)}</p>
            <span style="color:rgba(255,255,255,0.4);font-size:0.7rem;">${escapeHTML(item.game || '')}</span>
          </div>
          <span style="font-weight:600;font-size:0.8rem;white-space:nowrap;">${item.price.toFixed(0)} ₽</span>
          <button onclick="removeMiniCartItem(${item.id}, event)" style="background:none;border:none;color:rgba(255,255,255,0.3);cursor:pointer;padding:4px 6px;border-radius:6px;font-size:0.75rem;"><i class="fa-solid fa-xmark"></i></button>
        </div>`;
      });
      if (data.items.length > 5) html += `<p style="text-align:center;color:rgba(255,255,255,0.4);font-size:0.8rem;margin-top:0.5rem;">+ ещё ${escapeHTML(String(data.items.length - 5))} товара</p>`;
      content.innerHTML = html;
      total.style.display = 'block';
      total.innerHTML = `<div style="display:flex;justify-content:space-between;margin-bottom:0.5rem;font-size:0.9rem;"><span>Итого:</span><span style="font-weight:700;">${escapeHTML(String(data.total.toFixed(0)))} ₽</span></div>
        <a href="/cart" class="btn btn-primary btn-sm" style="width:100%;">Перейти в корзину</a>`;
    } else {
      content.innerHTML = `<div style="text-align:center;padding:1.5rem;"><i class="fa-solid fa-cart-shopping" style="font-size:2rem;color:rgba(255,255,255,0.15);display:block;margin-bottom:0.5rem;"></i><p style="color:rgba(255,255,255,0.3);margin:0;font-size:0.85rem;">Корзина пуста</p></div>`;
      total.style.display = 'none';
    }
  } catch (e) {}
}

async function removeMiniCartItem(id, event) {
  event?.preventDefault();
  event?.stopPropagation();
  await fetch('/api/cart/remove/' + id, { method: 'DELETE', headers: csrfHeaders() });
  loadMiniCart();
  loadCartCount();
  return false;
}

// ---------- Уведомления (колокольчик) ----------
async function loadNotifications() {
  try {
    const res = await fetch('/api/notifications');
    const notifs = await res.json();
    renderNotifications(notifs);
  } catch (e) {}
}

function renderNotifications(notifs) {
  const list = $('#notifList');
  if (!list) return;

  if (notifs?.length > 0) {
    const html = notifs.map(n => {
      const typeInfo = getNotificationType(n.text);
      const time = timeAgo(n.created_at);
      const safeText = escapeHTML(n.text);
      // Безопасная ссылка: только внутренние пути
      const link = (n.link && (n.link.startsWith('/') || n.link.startsWith('#'))) ? n.link : '#';
      return `<a href="${link}" onclick="markOneRead(${parseInt(n.id, 10)})" 
        class="notif-item${n.is_read ? '' : ' unread'}"
        style="background:${typeInfo.gradient};border:1px solid ${typeInfo.color}22;"
        onmouseover="this.style.transform='translateX(4px)';this.style.borderColor='${typeInfo.color}55'"
        onmouseout="this.style.transform='none';this.style.borderColor='${typeInfo.color}22'">
        <div class="notif-icon" style="background:${typeInfo.color}22;">
          <i class="${typeInfo.icon}" style="color:${typeInfo.color};"></i>
        </div>
        <div class="notif-content">
          <p class="notif-text">${safeText}</p>
          <span class="notif-time" style="color:${typeInfo.color};">
            <i class="fa-solid fa-clock" style="font-size:0.65rem;"></i> ${escapeHTML(time)}
          </span>
        </div>
      </a>`;
    }).join('');
    list.innerHTML = `<div style="display:flex;flex-direction:column;gap:6px;">${html}</div>`;
  } else {
    list.innerHTML = `<div class="notif-empty">
      <div class="notif-empty-icon"><i class="fa-solid fa-bell" style="font-size:2rem;color:rgba(139,92,246,0.3);"></i></div>
      <p style="font-size:1rem;font-weight:600;color:rgba(255,255,255,0.5);margin:0 0 4px;">Нет новых уведомлений</p>
      <p style="font-size:0.8rem;color:rgba(255,255,255,0.25);margin:0;">Здесь будут появляться важные события</p>
    </div>`;
  }
}

function getNotificationType(text) {
  const patterns = [
    { test: /💰|Выплата/, icon: 'fa-solid fa-coins', color: '#10b981', gradient: 'linear-gradient(135deg, rgba(16,185,129,0.2), rgba(16,185,129,0.05))' },
    { test: /📦|заказ|Заказ/, icon: 'fa-solid fa-box', color: '#3b82f6', gradient: 'linear-gradient(135deg, rgba(59,130,246,0.2), rgba(59,130,246,0.05))' },
    { test: /⭐|отзыв|Отзыв/, icon: 'fa-solid fa-star', color: '#f59e0b', gradient: 'linear-gradient(135deg, rgba(245,158,11,0.2), rgba(245,158,11,0.05))' },
    { test: /💬|сообщение/, icon: 'fa-solid fa-comment-dots', color: '#a78bfa', gradient: 'linear-gradient(135deg, rgba(167,139,250,0.2), rgba(167,139,250,0.05))' },
    { test: /✅|одобрен|подтвержд/, icon: 'fa-solid fa-circle-check', color: '#10b981', gradient: 'linear-gradient(135deg, rgba(16,185,129,0.2), rgba(16,185,129,0.05))' },
    { test: /❌|отклон|отказан/, icon: 'fa-solid fa-circle-xmark', color: '#ef4444', gradient: 'linear-gradient(135deg, rgba(239,68,68,0.2), rgba(239,68,68,0.05))' }
  ];
  const found = patterns.find(p => p.test.test(text));
  return found || { icon: 'fa-solid fa-bell', color: '#8b5cf6', gradient: 'linear-gradient(135deg, rgba(139,92,246,0.15), rgba(139,92,246,0.05))' };
}

async function markAllRead() {
  await fetch('/api/notifications/read-all', { method: 'POST', headers: csrfHeaders() });
  $('#notifList').innerHTML = `<div class="notif-empty">
    <div class="notif-empty-icon"><i class="fa-solid fa-check-circle" style="font-size:2rem;color:rgba(16,185,129,0.3);"></i></div>
    <p style="font-size:1rem;font-weight:600;color:rgba(255,255,255,0.5);margin:0 0 4px;">Всё прочитано!</p></div>`;
  $('#notifBadge').style.display = 'none';
}

async function markOneRead(id) {
  await fetch('/api/notifications/read/' + id, { method: 'POST', headers: csrfHeaders() });
  loadNotifications();
  loadNotifCount();
}

async function loadNotifCount() {
  try {
    const res = await fetch('/api/notifications/count');
    const data = await res.json();
    const badge = $('#notifBadge');
    if (!badge) return;
    if (data.count > 0) {
      badge.textContent = data.count > 99 ? '99+' : data.count;
      badge.style.display = 'flex';
    } else {
      badge.style.display = 'none';
    }
  } catch (e) {}
}

// ---------- Лайки ----------
async function toggleLike(reviewID) {
  try {
    const res = await fetch('/api/review/' + reviewID + '/like', { method: 'POST', headers: csrfHeaders() });
    const data = await res.json();
    const btn = $('#like-' + reviewID);
    const count = $('#like-count-' + reviewID);
    const icon = btn?.querySelector('i');
    if (btn) {
      btn.classList.toggle('liked', data.liked);
      if (icon) icon.className = data.liked ? 'fa-solid fa-heart' : 'fa-regular fa-heart';
    }
    if (count) count.textContent = data.count;
  } catch (e) {}
}

async function loadLikes() {
  $$('.like-btn').forEach(async btn => {
    const reviewID = btn.id.replace('like-', '');
    try {
      const res = await fetch('/api/review/' + reviewID + '/likes');
      const data = await res.json();
      const count = $('#like-count-' + reviewID);
      const icon = btn.querySelector('i');
      if (count) count.textContent = data.count;
      if (data.liked) {
        btn.classList.add('liked');
        if (icon) icon.className = 'fa-solid fa-heart';
      }
    } catch (e) {}
  });
}

// ---------- Прочее ----------
async function releaseEscrow(orderID) {
  if (!confirm('Подтвердить выполнение заказа? Средства будут переведены продавцу.')) return;
  try {
    const res = await fetch('/api/escrow/release/' + orderID, { method: 'POST', headers: csrfHeaders() });
    const data = await res.json();
    showNotification(data.message, data.success ? 'success' : 'error');
    if (data.success) setTimeout(() => location.reload(), 1000);
  } catch (e) {}
}

function becomeSeller(e) {
  e.preventDefault();
  window.location.href = isLoggedIn() ? '/add-boost' : '/register';
}

async function verifySeller(id) {
  await fetch('/api/admin/verify/' + id);
  location.reload();
}

// ---------- Аудио ----------
function toggleDropdownSound() {
  const muted = AudioEngine?.isMuted?.() ?? false;
  muted ? AudioEngine.unmute() : AudioEngine.mute();
  updateSoundDropdownUI(!muted);
}

function updateSoundDropdownUI(muted) {
  const icon = $('#soundIconDropdown');
  const label = $('#soundLabelDropdown');
  const item = $('#soundToggleDropdown');
  if (!icon || !label || !item) return;
  if (muted) {
    icon.className = 'fa-solid fa-volume-xmark';
    label.textContent = 'Звук выключен';
    item.classList.add('muted');
  } else {
    icon.className = 'fa-solid fa-volume-high';
    label.textContent = 'Звук включен';
    item.classList.remove('muted');
  }
}
//------------ Чек-тест проверка пароля при регистрации--------------------






// ---------- 3D фон ----------
function initThreeJS() {
  if (typeof THREE === 'undefined') return;
  const canvas = $('#bg-canvas');
  if (!canvas) return;

  const renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: true });
  renderer.setPixelRatio(window.devicePixelRatio);
  renderer.setSize(window.innerWidth, window.innerHeight);

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(75, window.innerWidth / window.innerHeight, 0.1, 1000);
  camera.position.z = 30;

  const starsGeo = new THREE.BufferGeometry();
  const starsCount = 2000;
  const starsPos = new Float32Array(starsCount * 3);
  for (let i = 0; i < starsCount * 3; i += 3) {
    starsPos[i] = (Math.random() - 0.5) * 100;
    starsPos[i + 1] = (Math.random() - 0.5) * 60;
    starsPos[i + 2] = (Math.random() - 0.5) * 40;
  }
  starsGeo.setAttribute('position', new THREE.BufferAttribute(starsPos, 3));
  const starsMat = new THREE.PointsMaterial({ color: 0xffffff, size: 0.15, transparent: true, opacity: 0.8, blending: THREE.AdditiveBlending, depthWrite: false });
  const stars = new THREE.Points(starsGeo, starsMat);
  scene.add(stars);

  const colorStarsGeo = new THREE.BufferGeometry();
  const colorStarsCount = 500;
  const colorStarsPos = new Float32Array(colorStarsCount * 3);
  const colorStarsCol = new Float32Array(colorStarsCount * 3);
  const colors = [[0.545, 0.361, 0.965], [0.753, 0.518, 0.988], [0.506, 0.553, 0.973], [0.655, 0.486, 0.980]];
  for (let i = 0; i < colorStarsCount * 3; i += 3) {
    colorStarsPos[i] = (Math.random() - 0.5) * 100;
    colorStarsPos[i + 1] = (Math.random() - 0.5) * 60;
    colorStarsPos[i + 2] = (Math.random() - 0.5) * 40;
    const c = colors[Math.floor(Math.random() * colors.length)];
    colorStarsCol[i] = c[0];
    colorStarsCol[i + 1] = c[1];
    colorStarsCol[i + 2] = c[2];
  }
  colorStarsGeo.setAttribute('position', new THREE.BufferAttribute(colorStarsPos, 3));
  colorStarsGeo.setAttribute('color', new THREE.BufferAttribute(colorStarsCol, 3));
  const colorStarsMat = new THREE.PointsMaterial({ size: 0.2, vertexColors: true, transparent: true, opacity: 0.6, blending: THREE.AdditiveBlending, depthWrite: false });
  const colorStars = new THREE.Points(colorStarsGeo, colorStarsMat);
  scene.add(colorStars);

  function animate() {
    requestAnimationFrame(animate);
    stars.rotation.y += 0.0002;
    stars.rotation.x += 0.0001;
    colorStars.rotation.y -= 0.0003;
    colorStars.rotation.x -= 0.00015;
    renderer.render(scene, camera);
  }
  animate();

  window.addEventListener('resize', () => {
    camera.aspect = window.innerWidth / window.innerHeight;
    camera.updateProjectionMatrix();
    renderer.setSize(window.innerWidth, window.innerHeight);
  });
}

// ---------- Навбар скролл ----------
window.addEventListener('scroll', () => {
  const navbar = $('.navbar');
  navbar?.classList.toggle('scrolled', window.scrollY > 50);
});

// ---------- Применение промокода ----------
let appliedPromo = null;
async function applyPromo() {
  const code = $('#promoCode').value;
  if (!code) return;
  const total = parseFloat($('#totalPrice').textContent.replace(/[^\d]/g, ''));
  try {
    const res = await fetch('/api/promo/check', {
      method: 'POST',
      headers: csrfHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ code, amount: total })
    });
    const data = await res.json();
    const result = $('#promoResult');
    if (data.success) {
      result.innerHTML = `<span style="color:#10b981;">✅ ${escapeHTML(data.message)} (-${escapeHTML(String(data.discount_amount.toFixed(0)))} ₽)</span>`;
      $('#totalPrice').textContent = data.final_amount.toFixed(0) + ' ₽';
      appliedPromo = data;
    } else {
      result.innerHTML = `<span style="color:#ef4444;">❌ ${escapeHTML(data.message)}</span>`;
    }
  } catch (e) {}
}

// ---------- Плавные переходы страниц ----------
document.addEventListener('click', (e) => {
  const link = e.target.closest('a');
  if (link?.href?.startsWith(window.location.origin) && !link.hasAttribute('download') && !link.getAttribute('onclick')) {
    e.preventDefault();
    $('.main-content')?.classList.add('page-out');
    setTimeout(() => { window.location.href = link.href; }, 300);
  }
});

// ---------- PRO стиль ----------
fetch('/api/check-pro').then(r => r.json()).then(data => {
  if (data.is_pro) $('.user-dropdown-trigger')?.classList.add('pro-user');
});

// ---------- Инициализация при загрузке ----------
document.addEventListener('DOMContentLoaded', () => {
  initThreeJS();
  loadCartCount();
  loadNotifCount();
  initTheme();
  loadLikes();
  if (typeof AudioEngine !== 'undefined') updateSoundDropdownUI(AudioEngine.isMuted());

  setInterval(loadNotifCount, 10000);
  setInterval(loadCartCount, 30000);
});

// ---------- Экспорт в глобальную область ----------
Object.assign(window, {
  toggleDropdown, toggleNotifications, toggleMobileMenu, toggleTheme, toggleLike,
  addToCart, removeFromCart, clearCart, checkout, deleteBoost,
  showNotification, releaseEscrow, becomeSeller, verifySeller,
  markAllRead, markOneRead, loadNotifications, loadNotifCount,
  applyPromo, showMiniCart, startHideTimer, cancelHideTimer, removeMiniCartItem,
  toggleDropdownSound, playNotifySound
});


// Проверка пароля
function checkPasswordStrength() {
    const password = document.getElementById('password').value;
    const bars = document.querySelectorAll('#strengthBars .password-strength-bar');
    const text = document.getElementById('strengthText');
    const input = document.getElementById('password');
    
    // Сброс
    bars.forEach(bar => {
        bar.classList.remove('active', 'weak', 'medium', 'strong');
    });
    input.classList.remove('error', 'valid');
    
    if (!password) {
        text.textContent = '';
        return;
    }
    
    let strength = 0;
    
    // Длина
    if (password.length >= 8) strength++;
    if (password.length >= 12) strength++;
    
    // Символы
    if (/[a-z]/.test(password) && /[A-Z]/.test(password)) strength++;
    if (/\d/.test(password)) strength++;
    if (/[!@#$%^&*(),.?":{}|<>]/.test(password)) strength++;
    
    // Подсветка
    const activeBars = Math.min(4, Math.max(1, Math.floor(strength * 0.8)));
    
    for (let i = 0; i < activeBars; i++) {
        bars[i].classList.add('active');
        if (activeBars <= 2) {
            bars[i].classList.add('weak');
        } else if (activeBars === 3) {
            bars[i].classList.add('medium');
        } else {
            bars[i].classList.add('strong');
        }
    }
    
    if (activeBars <= 2) {
        text.textContent = 'Слабый пароль';
        text.style.color = '#f87171';
    } else if (activeBars === 3) {
        text.textContent = 'Средний пароль';
        text.style.color = '#fbbf24';
    } else {
        text.textContent = 'Надёжный пароль';
        text.style.color = '#6ee7b7';
        input.classList.add('valid');
    }
}

// Валидация формы
function validateForm() {
    const username = document.getElementById('username');
    const email = document.getElementById('email');
    const password = document.getElementById('password');
    const submitBtn = document.getElementById('submitBtn');
    let valid = true;
    
    // Сброс ошибок
    [username, email, password].forEach(el => el.classList.remove('error'));
    
    // Проверка имени
    if (username.value.trim().length < 3) {
        username.classList.add('error');
        valid = false;
    }
    
    // Проверка email
    const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
    if (!emailRegex.test(email.value)) {
        email.classList.add('error');
        valid = false;
    }
    
    // Проверка пароля
    if (password.value.length < 8) {
        password.classList.add('error');
        valid = false;
    }
    
    if (!valid) {
        return false;
    }
    
    // Блокировка кнопки от двойной отправки
    submitBtn.disabled = true;
    submitBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Создаём...';
    
    setTimeout(() => {
        if (submitBtn.disabled) {
            submitBtn.disabled = false;
            submitBtn.innerHTML = '<i class="fa-solid fa-rocket"></i> Создать аккаунт';
        }
    }, 10000);
    
    return true;
}

// Автофокус на первое поле
document.getElementById('username').focus();

//--------- Проверка на странице логина(входа в аккаунт) ---------
function handleLogin() {
    const username = document.getElementById('username');
    const password = document.getElementById('password');
    const submitBtn = document.getElementById('submitBtn');
    let valid = true;

    // Сброс ошибок
    [username, password].forEach(el => el.classList.remove('error'));

    // Проверка на пустоту
    if (!username.value.trim()) {
        username.classList.add('error');
        valid = false;
    }
    if (!password.value) {
        password.classList.add('error');
        valid = false;
    }

    if (!valid) return false;

    // Блокировка от двойной отправки
    submitBtn.disabled = true;
    submitBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Входим...';

    // Разблокировка через 10 секунд на случай ошибки сети
    setTimeout(() => {
        if (submitBtn.disabled) {
            submitBtn.disabled = false;
            submitBtn.innerHTML = '<i class="fa-solid fa-rocket"></i> Войти';
        }
    }, 10000);

    return true;
}

// Автофокус на поле ввода
document.getElementById('username').focus();


//--------- Проверка на странице forgot-password(восстановление пароля) ---------

function handleForgot() {
    const email = document.getElementById('email');
    const submitBtn = document.getElementById('submitBtn');
    let valid = true;

    email.classList.remove('error');

    const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
    if (!emailRegex.test(email.value)) {
        email.classList.add('error');
        valid = false;
    }

    if (!valid) return false;

    submitBtn.disabled = true;
    submitBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Отправляем...';

    setTimeout(() => {
        if (submitBtn.disabled) {
            submitBtn.disabled = false;
            submitBtn.innerHTML = '<i class="fa-solid fa-paper-plane"></i> Отправить код';
        }
    }, 10000);

    return true;
}

document.getElementById('email')?.focus();



//---------- Проверка на странице dispute-open(Открытие спора) ---------
function handleDisputeSubmit() {
    const reason = document.getElementById('reason');
    const description = document.getElementById('description');
    const submitBtn = document.getElementById('submitBtn');
    let valid = true;

    // Сброс ошибок
    [reason, description].forEach(el => el.classList.remove('error'));

    if (!reason.value) {
        reason.classList.add('error');
        valid = false;
    }
    if (!description.value.trim()) {
        description.classList.add('error');
        valid = false;
    }

    if (!valid) return false;

    // Блокировка от двойной отправки
    submitBtn.disabled = true;
    submitBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Открываем...';

    setTimeout(() => {
        if (submitBtn.disabled) {
            submitBtn.disabled = false;
            submitBtn.innerHTML = '<i class="fa-solid fa-flag"></i> Открыть спор';
        }
    }, 10000);

    return true;
}


