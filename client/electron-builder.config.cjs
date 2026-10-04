/**
 * electron-builder 配置。
 *
 * 用 .cjs 而不是 .ts：electron-builder 编译 TS 配置时会往
 * ~/.cache/config-file-ts/ 建 node_modules 符号链接，非管理员权限下经常
 * EPERM 失败（"operation not permitted, symlink"）。纯 CJS 配置直接 require，
 * 不需要编译，也就不受这个限制。
 *
 * 关键点：Go 核心二进制必须随包分发。这里用 extraResources 把它放到
 * resources/core/，与 CoreProcess.candidates() 的查找路径对应，
 * 同时把默认的 agent.yaml 模板一起带上（首次运行会复制到用户数据目录）。
 *
 * @type {import('electron-builder').Configuration}
 */
const config = {
  appId: 'com.codeporter.client',
  productName: 'CodePorter',
  directories: {
    output: 'release',
    buildResources: 'build'
  },
  files: [
    'dist-main/**/*',
    'dist/electron/**/*',
    'package.json'
  ],
  extraResources: [
    {
      from: '../backend/bin',
      to: 'core',
      filter: ['**/*']
    },
    {
      from: '../backend/configs/agent.yaml',
      to: 'core/configs/agent.yaml'
    }
  ],
  win: {
    // 只要免安装版：单个 exe 双击即用，不写注册表、不需要 NSIS 工具链。
    // 需要安装版时临时加回来即可：
    //   npx electron-builder --win nsis --x64 --config electron-builder.config.cjs
    target: [
      { target: 'portable', arch: ['x64'] }
    ],
    artifactName: 'CodePorter-${version}-portable.${ext}'
  },
  portable: {
    artifactName: 'CodePorter-${version}-portable.${ext}'
  },
  mac: {
    // dmg 是分发形态，zip 方便 CI 直接上传（解压即得 .app）。
    // 注意：dmg 只能在与 macOS 同类的系统上产出（依赖 hdiutil），
    // Linux/Windows 打不出来，需要它就用 CI 的 macos runner。
    target: ['dmg', 'zip'],
    category: 'public.app-category.developer-tools',
    artifactName: 'CodePorter-${version}-mac-${arch}.${ext}',
    // 没有 Apple 开发者证书时不要去探测签名身份，否则打包会失败。
    // 有证书时删掉这两行，并配置 CSC_LINK / CSC_KEY_PASSWORD 环境变量即可。
    identity: null,
    gatekeeperAssess: false
  },
  linux: {
    target: ['AppImage'],
    category: 'Development',
    artifactName: 'CodePorter-${version}-linux-${arch}.${ext}'
  },
  // Go 核心不参与 asar 打包（走 extraResources），这里排除掉避免重复打包。
  asar: true,
  npmRebuild: false
}

module.exports = config
