## 版本发布流程
1. 修改frontend/package.json 里面的版本号
2. 提交到Github
3. Github新建tag后自动编译发布版本

## PWA图标

更新图标 ：以后想换图标，只需替换根目录 ZacpAPP.jpg （或改脚本源路径），重跑 go run ./scripts/pwa-icons/main.go <源图> frontend/public/icons 重新生成即可。
