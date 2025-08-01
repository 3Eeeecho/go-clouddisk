# 待实现功能

* ✅**文件重命名：** 修改文件的 `FileName` 字段。
* ✅**文件移动/复制：** 修改文件的 `ParentFolderID`。
* ✅**回收站功能：** 一个单独的接口来列出 `status != 1` 的文件，并允许恢复 (`status` 改回 `1`) 或永久删除（从数据库和物理存储中彻底删除）。
* ✅**文件夹下载：** 可以下载整个文件夹的内容
* ✅**更完善的错误处理：** 细化 `xerr` 中的错误码和信息。
* ✅**多存储后端支持：** 支持 MinIO/S3 以及阿里云以外的更多云存储服务。
* **文件分享：** 生成一个带有时效或密码的公开链接，允许非认证用户下载。
* **缩略图生成：** 对于图片和视频，上传后自动生成缩略图。
* **用户配额管理：** 限制每个用户的存储空间。
* **搜索功能：** 根据文件名或 MIME 类型搜索文件。
* **Websocket 通知：** 例如，文件上传完成时给前端发送通知。

**代码优化/改进：**

* ✅**配置外部化：** 使用 Viper 等库来更灵活地管理配置。
* ✅**日志：** 引入更专业的日志库Zap，而不是默认的 `log` 包。
* ✅**数据库事务：** 在涉及到多个数据库操作时，确保使用事务来保证数据一致性。
* **测试：** 为服务层和处理器层编写单元测试和集成测试。
* **依赖注入框架：** 对于大型项目，可以考虑使用依赖注入框架（如 Wire）。
* **CORS 配置：** 如果是前后端分离，确保 CORS 配置正确。
