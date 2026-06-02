import Vue from 'vue'
import Router from 'vue-router'
import Overview from '../components/Overview.vue'
import Configure from '../components/Configure.vue'

Vue.use(Router)

export default new Router({
    // frpc 管理界面路由：
    // / 展示代理运行状态，对应后端 client/admin_api.go:apiStatus()
    // /configure 展示和更新配置，对应 client/admin_api.go:apiGetConfig()、apiPutConfig()
    routes: [{
        path: '/',
        name: 'Overview',
        component: Overview
    },{
        path: '/configure',
        name: 'Configure',
        component: Configure,
    }]
})
