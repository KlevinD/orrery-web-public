# GitHub 发布策略

自 2026-10-19 起，Orrery 停用 GitHub Actions CI/CD。仓库级 Actions 已关闭，
GitHub 中登记的历史工作流已禁用；主分支不再保存可执行的工作流定义。
push、PR、tag、定时和 workflow_dispatch 均不用于测试、构建或部署。

后续按[手动验证与部署流程](../orrery_v2/docs/operations/manual-deployment.md)操作。
测试、构建、签名、业务审批、发布锁、版本核对及回滚要求继续执行，
实际结果由执行者记录。GitHub 不显示 CI 检查不能当作测试通过。

没有用户明确要求，不重新启用 Actions、不恢复旧 workflow、不安装自动发布 cron。
旧工作流可从 Git 历史查阅，不作为当前发布入口。
