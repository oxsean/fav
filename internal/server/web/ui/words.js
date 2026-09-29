// words are the texts of the shared components.
import {register} from '../core/i18n.js';

register('ui', {
  'nav.home': ['首页', 'Home'], 'nav.tasks': ['任务', 'Tasks'], 'nav.runs': ['运行', 'Runs'], 'nav.machines': ['机器', 'Machines'],
  'nav.agents': ['Agent', 'Agents'], 'nav.team': ['团队', 'Team'], 'nav.me': ['我', 'Me'], 'nav.waiting': ['等你', 'Waiting'],
  'nav.label': ['页面', 'Pages'], 'nav.collapse': ['收起菜单', 'Collapse the menu'], 'nav.expand': ['展开菜单', 'Expand the menu'],
  'shell.search': ['搜索任务、运行、机器，或输入命令', 'Search tasks, runs, machines, or type a command'],
  'shell.new': ['新建任务', 'New task'], 'shell.running': ['在跑', 'Running'], 'shell.queued': ['排队', 'Queued'],
  'shell.offlineOne': ['%s 离线', '%s offline'], 'shell.offlineMany': ['%d 台机器离线', '%d machines offline'],
  'shell.servers': ['切换 server', 'Switch server'], 'shell.keys': ['按键', 'Keys'], 'shell.counts': ['在跑 %d · 排队 %d', '%d running · %d queued'],
  'shell.spent': ['今天 %s tok%s', 'Today %s tok%s'],
  'banner.offline': ['连接断开，正在重连…', 'Connection lost, reconnecting…'], 'banner.retry': ['立即重连', 'Reconnect now'],
  'banner.outdated': ['服务器已经更新，刷新页面后继续。', 'The server was updated: reload the page to go on.'], 'banner.reload': ['刷新', 'Reload'],
  'ui.close': ['关闭', 'Close'], 'ui.serverAdmin': ['服务器管理员', 'Server admin'], 'ui.back': ['返回', 'Back'], 'ui.undo': ['撤销', 'Undo'], 'ui.dismiss': ['知道了', 'Dismiss'],
  'ui.expand': ['展开', 'Expand'], 'ui.collapse': ['收起', 'Collapse'], 'ui.sortBy': ['按%s排序', 'Sort by %s'], 'ui.empty': ['没有内容', 'Nothing here'],
  'status.queued': ['排队', 'Queued'], 'status.starting': ['启动中', 'Starting'], 'status.running': ['运行中', 'Running'],
  'status.unknown': ['状态不明', 'Unknown'], 'status.exited': ['已结束', 'Exited'], 'status.stopped': ['已停止', 'Stopped'],
  'status.failed': ['失败', 'Failed'], 'status.canceled': ['已取消', 'Canceled'], 'status.abandoned': ['已放弃', 'Abandoned'],
  'status.asked': ['在问你', 'Asking you'], 'status.permission': ['要你批准', 'Needs approval'], 'status.stalled': ['卡住了', 'Stalled'],
  'status.backlog': ['待办', 'Backlog'], 'status.todo': ['未开始', 'To do'], 'status.done': ['完成', 'Done'], 'status.waiting': ['等你', 'Waiting'],
  'chart.running': ['在跑', 'Running'], 'chart.exited': ['正常结束', 'Ended well'], 'chart.failed': ['出错', 'Failed'], 'chart.stopped': ['停止', 'Stopped'],
  'palette.hint': ['输入操作、任务、运行或机器…', 'Type an action, a task, a run or a machine…'], 'palette.none': ['没有匹配的', 'Nothing matches'],
  'palette.task': ['任务', 'Task'], 'palette.run': ['运行', 'Run'], 'palette.machine': ['机器', 'Machine'],
  'status.online': ['在线', 'Online'], 'status.offline': ['离线', 'Offline'],
  'picker.find': ['输入以查找', 'Type to find'], 'picker.none': ['没有匹配的', 'Nothing matches'], 'picker.nothing': ['未选', 'None'],
  'picker.done': ['完成', 'Done'], 'ui.prev': ['上一条', 'Previous'], 'ui.next': ['下一条', 'Next'],
});
