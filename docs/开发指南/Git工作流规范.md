# Git 工作流规范 (Solo Developer Version)

作为独立开发者，你的 Git 流程应专注于**备份**和**实验**，而不是繁琐的审批。

## 1. 极简分支策略

| 分支名 | 说明 | 策略 |
|--------|------|------|
| `main` | 你的"保险箱" | 永远保持可运行状态。里程碑完成后才 Push。 |
| `dev` | 你的"试验台" | 日常开发分支，哪怕代码很乱也可以随时 Commit。 |

## 2. 三步走开发循环

### 2.1 实验 (Feature)
当你想要开发一个大功能（比如登录）时，建议切出临时分支，避免搞坏 `dev`。
```bash
git checkout -b feature/login
```

### 2.2 存盘 (Commit)
**把 Commit 当作游戏的存盘点。**
- 刚写完一个函数？ `git commit -m "wip: add login func"`
- 刚调通一个接口？ `git commit -m "feat: login api works"`
- **不用担心 commit 太多**，你是唯一的用户。

### 2.3 归档 (Merge)
功能完成后，合并回 `dev` 或 `main`。
```bash
git checkout main
git merge feature/login
git branch -d feature/login
```

## 3. 救命锦囊

- **代码写乱了，想重来**：
    `git reset --hard HEAD` (危险：会丢弃所有未提交的修改)
- **误删了文件**：
    `git checkout -- <filename>`
- **忘记刚写了什么**：
    `git diff`
