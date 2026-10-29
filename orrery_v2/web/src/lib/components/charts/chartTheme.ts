import * as echarts from 'echarts/core'

export const chartPalette = ['#38bdf8', '#22c55e', '#f59e0b', '#a78bfa', '#f43f5e']
const chartFontFamily =
  '"Noto Sans CJK SC", "Noto Sans SC", "PingFang SC", "Microsoft YaHei UI", "Microsoft YaHei", sans-serif'

echarts.registerTheme('orrery', {
  color: chartPalette,
  backgroundColor: 'transparent',
  textStyle: {
    color: '#b7c4d5',
    fontFamily: chartFontFamily
  },
  legend: {
    textStyle: {
      color: '#94a3b8'
    }
  },
  tooltip: {
    backgroundColor: '#111822',
    borderColor: '#293241',
    textStyle: {
      color: '#e5edf6'
    }
  }
})
