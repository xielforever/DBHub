import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import './style.css'
import { initTheme } from './utils/theme'
import { installButtonFeedback } from './utils/button-feedback'
import { dataLoading } from './utils/data-loading'
import DesignPreview from './views/design/DesignPreview.vue'

initTheme()
const app = createApp(DesignPreview)
const stopButtonFeedback = installButtonFeedback()
app.onUnmount(stopButtonFeedback)
if (import.meta.hot) import.meta.hot.dispose(stopButtonFeedback)
app.directive('data-loading', dataLoading)
app.use(ElementPlus, { locale: zhCn }).mount('#app')
