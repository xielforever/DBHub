# UI 设计规范 (Design Tokens)

DBHub 采用 **Liquid Glass (液态玻璃)** 设计语言。以下是核心设计令牌（Design Tokens），它们已在 Tailwind CSS v4 的 `style.css` 中定义。

## 1. 色彩体系 (Colors)

### 1.1 核心背景 (`bg-*`)
- `bg-premium-dark`: `#0f172a` (深邃蓝黑，主背景)
- `bg-premium-glass`: `rgba(255, 255, 255, 0.1)` (玻璃卡片背景)

### 1.2 边框 (`border-*`)
- `border-premium-border`: `rgba(255, 255, 255, 0.2)` (微弱的白色描边)

### 1.3 渐变 (Gradients)
- **Liquid Gradient**: `linear-gradient(135deg, #667eea 0%, #764ba2 100%)`
  - 用途：主按钮、品牌标识
- **Glass Gradient**: `linear-gradient(135deg, rgba(255, 255, 255, 0.1), rgba(255, 255, 255, 0.05))`
  - 用途：卡片高光

## 2. 效果 (Effects)

### 2.1 阴影 (Shadows)
- **Glass Shadow**: `0 8px 32px 0 rgba(31, 38, 135, 0.37)`
  - 用途：卡片浮起效果

### 2.2 模糊 (Blur)
- **Backdrop Blur**: `backdrop-blur-xl` (24px)
  - 用途：所有玻璃卡片背景，确保通透感

### 2.3 圆角 (Radius)
- `rounded-xl`: 1rem (按钮)
- `rounded-2xl`: 1.5rem (图标容器)
- `rounded-3xl`: 2.5rem (主要卡片)

## 3. 字体 (Typography)
- **Font Family**: System UI (San Francisco, Inter, Segoe UI)
- **Weight**:
  - Regular (400): 正文
  - Medium (500): 按钮文本
  - Bold (700): 标题

## 4. CSS 变量参考

```css
@theme {
  --color-premium-dark: #0f172a;
  --color-premium-glass: rgba(255, 255, 255, 0.1);
  --color-premium-border: rgba(255, 255, 255, 0.2);
  
  --image-liquid-gradient: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  
  --shadow-glass: 0 8px 32px 0 rgba(31, 38, 135, 0.37);
}
```
