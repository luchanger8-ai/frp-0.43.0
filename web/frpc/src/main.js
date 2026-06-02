import Vue from 'vue'
// frpc Admin UI 入口文件。
// 后端静态资源挂载位置：client/admin.go:RunAdminServer()。
// 页面 API 来源：client/admin_api.go，例如 /api/status、/api/reload、/api/config。
// import ElementUI from 'element-ui'
import {
    Button,
    Form,
    FormItem,
    Row,
    Col,
    Table,
    TableColumn,
    Menu,
    MenuItem,
    MessageBox,
    Message,
    Input
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
Vue.use(Menu)
Vue.use(MenuItem)
Vue.use(Input)

Vue.prototype.$msgbox = MessageBox;
Vue.prototype.$confirm = MessageBox.confirm
Vue.prototype.$message = Message

//Vue.use(ElementUI)

Vue.config.productionTip = false

new Vue({
    el: '#app',
    router,
    template: '<App/>',
    components: { App }
})
