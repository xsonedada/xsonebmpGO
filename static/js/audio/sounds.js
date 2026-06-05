// static/js/audio/sounds.js
import { audioEngine } from './engine.js';
import { synth } from './synth.js';

class SoundLibrary {
  constructor() {
    this.buffers = {};
    this.loaded = false;
  }

  // Генерация всех звуков при инициализации
  async load() {
    audioEngine.init();
    
    this.buffers = {
      hover: synth.generateHoverSound(),
      click: synth.generateClickSound(),
      success: synth.generateSuccessSound(),
      error: synth.generateErrorSound(),
      sweepUp: synth.generateSweepSound('up'),
      sweepDown: synth.generateSweepSound('down'),
      achievement: synth.generateAchievementSound(),
      cosmicPad: synth.generateCosmicPad(3.0),
      ambientDrone: synth.generateAmbientDrone(55),
    };
    
    this.loaded = true;
    console.log('🔊 Sound library loaded:', Object.keys(this.buffers).length, 'sounds');
  }

  // ========== ПУБЛИЧНЫЕ МЕТОДЫ ==========

  /** Наведение на интерактивный элемент */
  playHover() {
    audioEngine.playBuffer(this.buffers.hover, 0.6, 1.0);
  }

  /** Клик по элементу */
  playClick() {
    audioEngine.playBuffer(this.buffers.click, 0.7, 1.0);
  }

  /** Успешное действие (покупка, отправка) */
  playSuccess() {
    audioEngine.playBuffer(this.buffers.success, 0.8, 1.0);
  }

  /** Ошибка валидации или отклонение */
  playError() {
    audioEngine.playBuffer(this.buffers.error, 0.6, 1.0);
  }

  /** Открытие модального окна */
  playModalOpen() {
    audioEngine.playBuffer(this.buffers.sweepUp, 0.5, 1.0);
  }

  /** Закрытие модального окна */
  playModalClose() {
    audioEngine.playBuffer(this.buffers.sweepDown, 0.5, 1.0);
  }

  /** Получение достижения */
  playAchievement() {
    audioEngine.playBuffer(this.buffers.achievement, 0.9, 1.0);
  }

  /** Фоновый эмбиент (зацикленный) */
  playAmbient() {
    if (this.ambientSource) return; // Уже играет
    
    const ctx = audioEngine.ctx;
    this.ambientSource = ctx.createBufferSource();
    this.ambientSource.buffer = this.buffers.ambientDrone;
    this.ambientSource.loop = true;
    
    const gainNode = audioEngine.createChain(0.12);
    this.ambientSource.connect(gainNode);
    this.ambientSource.start();
  }

  stopAmbient() {
    if (this.ambientSource) {
      this.ambientSource.stop();
      this.ambientSource = null;
    }
  }

  /** "Космический" пэд для hero-секции (зацикленный) */
  playCosmicPad() {
    if (this.padSource) return;
    
    const ctx = audioEngine.ctx;
    this.padSource = ctx.createBufferSource();
    this.padSource.buffer = this.buffers.cosmicPad;
    this.padSource.loop = true;
    
    const gainNode = audioEngine.createChain(0.18);
    this.padSource.connect(gainNode);
    this.padSource.start();
  }

  stopCosmicPad() {
    if (this.padSource) {
      this.padSource.stop();
      this.padSource = null;
    }
  }
}

export const sounds = new SoundLibrary();