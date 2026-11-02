import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { getProjects, type Project } from '../api/client'
import { useToast } from '../hooks/useToast'
import HomeKnowledgeAssistant from './home/HomeKnowledgeAssistant'
import s from './Home.module.css'

// 首页 = 整页对话台（ADR-0038 决策 2、3）。
//
// 这里曾经是「记忆环 + 对话卡 + 项目速览 + 今日动态」四块东西各做一半的杂烩：
// 记忆环与仪表盘重复、动态卡的数字与仪表盘口径不一致、项目数在两处各算一遍。
// 现在首页只剩一件事——向知识库要东西：问，或搜。
//
// 数字、项目列表、交接记录都搬走了：数字只有仪表盘一处权威，记忆环归仪表盘，
// 知识页与笔记归「知识」tab。首页不再自己算任何统计。
export default function Home() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const pushToast = useToast()

  const [projects, setProjects] = useState<Project[]>([])
  const [projectsReady, setProjectsReady] = useState(false)

  useEffect(() => {
    document.title = 'Home - Repotory'
  }, [])

  useEffect(() => {
    let cancelled = false
    getProjects(undefined, false)
      .then(p => {
        if (cancelled) return
        setProjects(p ?? [])
        setProjectsReady(true)
      })
      .catch(() => {
        if (cancelled) return
        setProjects([])
        setProjectsReady(true)
      })
    return () => {
      cancelled = true
    }
  }, [])

  return (
    <div className={s.page}>
      <h1 className="visually-hidden">{t('home.consoleTitle', { defaultValue: '知识库对话台' })}</h1>

      <HomeKnowledgeAssistant
        projects={projects}
        projectsReady={projectsReady}
        onToast={pushToast}
        onGoToSettings={() => navigate('/settings')}
      />
    </div>
  )
}
