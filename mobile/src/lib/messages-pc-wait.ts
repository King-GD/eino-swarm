export const pcWaitEn = {
 "pcWait.title": "Waiting for a computer",
 "pcWait.hint": "Tasks need a computer to run commands and scripts. Install and open the desktop app, sign in to the same gateway account, and keep the computer online. Then connect below.",
 "pcWait.offline": "Your computers are offline. Open the desktop app and keep it connected to the network.",
 "pcWait.connect": "Online computers connect automatically. You can also retry below.",
 "pcWait.restoring": "The computer is connected. Waiting for its engine to become ready.",
} as const
export const pcWaitZh: Record<keyof typeof pcWaitEn,string> = {
 "pcWait.title": "等待电脑接入",
 "pcWait.hint": "任务需要在电脑上执行系统命令或脚本。请在电脑安装并启动应用，登录同一网关账号，并保持电脑在线，然后在下方连接。",
 "pcWait.offline": "电脑当前离线。请启动电脑端应用并保持联网。",
 "pcWait.connect": "在线电脑会自动连接，也可在下方重试。",
 "pcWait.restoring": "已连接电脑，正在等待引擎就绪。",
}
