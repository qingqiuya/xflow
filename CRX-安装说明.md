# 把插件打成 .crx 装（备选方案）

> 结论先说：**Windows 上的 Chrome / Edge 默认不收自签名 crx**，拖进去会被提示
> 「不是从商店安装的」并禁用。想真装上只有走企业策略一条路（下面第三节）。
> 如果只是想少一个文件夹，不值得折腾 —— 解压加载只是引用一个 40K 的目录。

## 一、产物

`dist/xflow-ext-<版本>.crx` + `dist/xflow-ext-<版本>.crx.sha256`

- **扩展 ID**：`jmadiadhfhdgfhmilhckbmdhbkpjplfc`（由私钥算出，私钥不变 ID 就不变）
- **私钥**：`~/localdl/ext-key.pem`（**不能丢**，丢了再打包就是另一个扩展，浏览器里的设置全废）
- 重新打包：`./pack-crx.sh`

## 二、直接装（先试这个，三十秒）

1. `chrome://extensions` → 打开「开发者模式」
2. 把 `.crx` 文件拖进页面

- **Linux** 上一般能成
- **Windows / macOS** 上基本会被禁用，扩展卡片变灰并提示「此扩展程序未在 Chrome 网上应用店中提供」

被禁用不用删，卸载掉继续用「加载已解压的扩展程序」即可。

## 三、企业策略强制安装（Windows 唯一可行解）

原理：往注册表写一条强制安装规则，Chrome 启动时从指定地址拉 update.xml，
校验 crx 签名后装上，并且用户不能禁用。

**前提**：扩展 ID 固定（就是上面那个）、crx 和 update.xml 放在 Chrome 能访问的地址。

### 1. 准备 update.xml

```xml
<?xml version='1.0' encoding='UTF-8'?>
<gupdate xmlns='http://www.google.com/update2/response' protocol='2.0'>
  <app appid='jmadiadhfhdgfhmilhckbmdhbkpjplfc'>
    <updatecheck
      codebase='http://example.lan/path/xflow-ext-1.1.3.crx'
      version='1.1.3' />
  </app>
</gupdate>
```

crx 和这个 xml 丢到任意**能通过 http 访问**的地方（内网文件服务、对象存储都行）。
**必须是 http/https，不能用 file://**。

### 2. 写注册表（管理员 PowerShell）

```powershell
$key = "HKLM:\SOFTWARE\Policies\Google\Chrome\ExtensionInstallForcelist"
New-Item -Path $key -Force | Out-Null
New-ItemProperty -Path $key -Name "1" `
  -Value "jmadiadhfhdgfhmilhckbmdhbkpjplfc;http://example.lan/path/update.xml" `
  -PropertyType String -Force
```

Edge 把 `Google\Chrome` 换成 `Microsoft\Edge`。

### 3. 生效

关掉**所有** Chrome 窗口再开（策略只在启动时读），
`chrome://policy` 里能看到这条规则，`chrome://extensions` 里扩展显示「由企业策略安装」。

### 4. 卸载

```powershell
Remove-ItemProperty -Path $key -Name "1"
```
然后重启 Chrome，手动移除扩展。

## 四、我的建议

家用没必要。解压加载的代价就是留一个 40K 目录，换来的好处是：
改代码直接刷新扩展就行，不用每次打包签名、不用维护 update.xml、不用碰注册表。

真要走正式分发（比如给家里人装），那也该上架商店，而不是自签名 crx。
