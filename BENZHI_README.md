# BENZHI_README

## 项目说明
- 项目：benzhi-project-3f8b5a24-4048-4dd2-8b16-5b1264824482
- 项目用途：古籍修复纸张适配资格管理 HTTP 服务，提供从建档到条件化、检测、复测裁决、授权放行及证据封存的可审计状态流程。
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 项目描述
- 项目名称：古籍修复纸张适配放行台
- 项目介绍：面向古籍修复实验室的纸张适配资格管理 HTTP 服务，将候选修复纸从用途建档、条件化处理、指标检测、双样一致性裁决推进到独立放行与证据封存，形成一条可审计且可复现的状态流程。项目按 standard 档规划不少于 2000 行真实生产 Go 代码、至少 20 个生产 Go 文件、6 个生产模块，不依赖外部业务系统。项目根目录必须提供简体中文 README.md，说明用途、标准构建、运行、测试和自检方式。
- 项目概述：面向古籍修复实验室的纸张适配资格管理 HTTP 服务，将候选修复纸从用途建档、条件化处理、指标检测、双样一致性裁决推进到独立放行与证据封存，形成一条可审计且可复现的状态流程。项目按 standard 档规划不少于 2000 行真实生产 Go 代码、至少 20 个生产 Go 文件、6 个生产模块，不依赖外部业务系统。项目根目录必须提供简体中文 README.md，说明用途、标准构建、运行、测试和自检方式。
- 核心工作流：研究员创建纸张适配案并声明古籍材质与修复用途，提交取样和判定方案后进入 PLAN_READY；完成温湿度条件化记录进入 CONDITIONED；录入两组盲样的强度、酸碱度、色差和纤维稳定性测量并通过完整性门禁进入 TESTED；系统生成差异项，研究员执行定向复测并由复核员裁决至 REVIEW_PENDING；授权人依据用途阈值作出放行决定进入 RELEASED，随后生成证据清单、摘要和审计校验结果并封存为 SEALED。任何检测不完整均不得前进，复测裁决不通过会明确退回 CONDITIONED，放行驳回会退回 TESTED 并保留原因和修订历史。
- 对外接口：仅提供版本化 HTTP JSON API，调用方通过 /api/v1/qualification-cases 及其子资源完成建档、方案提交、条件化确认、测量录入、复测裁决、放行和封存；服务支持 -addr=127.0.0.1:<port>，也可读取 PORT 并绑定 127.0.0.1:<PORT>，默认监听 127.0.0.1:19081，绝不默认绑定 0.0.0.0 或常见低位端口。

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...

cd /app && GOTOOLCHAIN=local go run ./cmd/server -self-check -addr=127.0.0.1:19081

cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh

./build_benzhi_docker.sh benzhi-project-3f8b5a24-4048-4dd2-8b16-5b1264824482-amd64 linux/amd64

./build_benzhi_docker.sh benzhi-project-3f8b5a24-4048-4dd2-8b16-5b1264824482-arm64 linux/arm64

docker run -it benzhi-project-3f8b5a24-4048-4dd2-8b16-5b1264824482-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/server -self-check -addr=127.0.0.1:19081`
