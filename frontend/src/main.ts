import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import './style.css'
import { dataLoading } from './utils/data-loading'
import App from './App.vue'
import router from './router'
import { initTheme } from './utils/theme'
import { installButtonFeedback } from './utils/button-feedback'

initTheme()

const app = createApp(App)
const stopButtonFeedback = installButtonFeedback()
app.onUnmount(stopButtonFeedback)
if (import.meta.hot) import.meta.hot.dispose(stopButtonFeedback)

app.use(createPinia())
app.use(router)
app.use(ElementPlus, { locale: zhCn })

app.directive('data-loading', dataLoading)
app.mount('#app')
