import Vue from 'vue'
// frps Dashboard UI 入口文件。
// 后端静态资源挂载位置：server/dashboard.go:RunDashboardServer()。
// 页面 API 来源：server/dashboard_api.go，例如 /api/serverinfo、/api/proxy/:type、/api/traffic/:name。
//import ElementUI from 'element-ui'
import {
    Button,
    Form,
    FormItem,
    Row,
    Col,
    Table,
    TableColumn,
    Popover,
    Menu,
    Submenu,
    MenuItem,
    Tag
} from 'element-ui'
import lang from 'element-ui/lib/locale/lang/en'
import locale from 'element-ui/lib/locale'
import 'element-ui/lib/theme-chalk/index.css'
import './utils/less/custom.less'

import App from './App.vue'
import router from './router'
import 'whatwg-fetch'

locale.use(lang)

Vue.use(Button)
Vue.use(Form)
Vue.use(FormItem)
Vue.use(Row)
Vue.use(Col)
Vue.use(Table)
Vue.use(TableColumn)
Vue.use(Popover)
Vue.use(Menu)
Vue.use(Submenu)
Vue.use(MenuItem)
Vue.use(Tag)

Vue.config.productionTip = false

new Vue({
    el: '#app',
    router,
    template: '<App/>',
    components: { App }
})
