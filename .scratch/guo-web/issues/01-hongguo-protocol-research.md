# 红果源协议考古

Status: resolved
Type: research

## Question

从 `/home/zhao/clone/guoapp`（只读参考）考古红果源协议全貌，写成 `docs/research/hongguo-protocol.md`（每条结论标注 guoapp 源码位置）：

1. 分类/筛选/排序的请求参数面（有哪些维度可筛、哪些可排序）
2. 搜索与联想（官网搜索 + 名称索引，防抖与分页方式）
3. 详情与分集列表的数据结构
4. 取流：播放地址获取、画质档位、线路选择
5. 请求签名/设备参数/headers 的生成逻辑与必要字段
6. HLS 分段结构与加密/解密机制（下载合并后为何能无加密——解码链路在代码哪一层）
7. 弹幕 API（端点、参数、响应结构）
8. guoapp 内置的排行榜接口（注明它与最新官方 App 排行榜的差异面，供抓包对照）

这份文档是浏览/搜索/详情/播放/下载各规格票的数据面依据。

## Answer

调研完成，成果：`docs/research/hongguo-protocol.md`（433 行，8 节 + 端点速查表 + 重写版最小协议面清单，每条结论标注 guoapp 源码位置）。

要点：
- 红果有**两条协议面**：官网 SSR（`hongguoduanju.com`，`_ROUTER_DATA` JSON：分类兜底/搜索/详情兜底/网页取流/全部 4 个榜单）与字节 App API（`api5-normal-sinfonlineb.fqnovel.com`，Gorgon 签名：目录 feed（offset+session_id 游标、18 条/页）/详情分集/取流/弹幕）；另有第三方兜底取流 API（`v2.` 加密信封 + `spade_a` CENC 密钥混淆算法完整还原）。
- 取流为 **Web→App→兜底三级回退**（App 侧 bytevc 降权策略）。
- **CENC 解密不在 Go 层**——Go 只提取密钥，解密落在 libmpv/ffmpeg demux 层（`decryption_key`），这解释了「下载存密文、合并产物无加密」：重写版下载合并必须走同一条 ffmpeg 解密链路。
- 弹幕走评论接口（Comment-Source/Server-Channel/X-Argus/X-Ladon 全套签名，30 秒窗口游标）。
- 第 8 节给出 guoapp 榜单（官网 4 榜、20 条/页）与官方 App 榜单的差异面 + 8 条抓包对照清单（供「官方红果排行榜抓包」票对照）。
