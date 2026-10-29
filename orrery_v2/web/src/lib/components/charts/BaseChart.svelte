<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import * as echarts from 'echarts/core'
  import { BarChart, GraphChart, LineChart, RadarChart } from 'echarts/charts'
  import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
  import { CanvasRenderer } from 'echarts/renderers'
  import type { EChartsCoreOption } from 'echarts/core'

  echarts.use([BarChart, GraphChart, GridComponent, LegendComponent, LineChart, RadarChart, TooltipComponent, CanvasRenderer])

  interface Props {
    option: EChartsCoreOption
    height?: string
  }

  let { option, height = '260px' }: Props = $props()
  let host: HTMLDivElement
  let chart: echarts.ECharts | undefined

  onMount(() => {
    chart = echarts.init(host, 'orrery')
    chart.setOption(option)
    const resize = () => chart?.resize()
    window.addEventListener('resize', resize)

    return () => {
      window.removeEventListener('resize', resize)
    }
  })

  $effect(() => {
    chart?.setOption(option, true)
  })

  onDestroy(() => {
    chart?.dispose()
  })
</script>

<div bind:this={host} style:height class="w-full"></div>
