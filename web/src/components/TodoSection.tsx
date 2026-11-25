import { useTranslation } from 'react-i18next'
import { useState, useEffect, useCallback, useRef } from 'react'
import { listTodos, createTodo, toggleTodo, deleteTodo, reorderTodos, type Todo } from '../api/client'
import { useConfirmClick } from '../hooks/useConfirmClick'
import ErrorBanner from './ErrorBanner'
import Icon from './Icon'
import s from './TodoSection.module.css'
import { describeApiError } from '../utils/apiError'

interface Props {
  projectId: number
}

function TodoSection({ projectId }: Props) {
  const { t } = useTranslation()
  const [todos, setTodos] = useState<Todo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [title, setTitle] = useState('')
  const [adding, setAdding] = useState(false)

  // Sequence guard: a late response for the previous project must not
  // overwrite the current list (see NoteSection for the same pattern).
  const fetchSeqRef = useRef(0)
  const fetchTodos = useCallback(() => {
    const seq = ++fetchSeqRef.current
    listTodos(projectId)
      .then(todos => { if (fetchSeqRef.current === seq) setTodos(todos) })
      .catch(e => {
        // A failed load must not masquerade as the "no todos" empty state.
        if (fetchSeqRef.current === seq) setError(`${t('project.loadFailed')}: ${describeApiError(t, e)}`)
      })
      .finally(() => { if (fetchSeqRef.current === seq) setLoading(false) })
  }, [projectId, t])

  useEffect(() => { fetchTodos() }, [fetchTodos])

  // Listen for todo-changed events (e.g. after delete from TodoDeleteButton)
  useEffect(() => {
    const handler = () => fetchTodos()
    window.addEventListener('repotory:todos-changed', handler)
    return () => window.removeEventListener('repotory:todos-changed', handler)
  }, [fetchTodos])

  const handleAdd = async () => {
    if (!title.trim()) return
    setAdding(true)
    try {
      await createTodo(projectId, title.trim())
      setTitle('')
      fetchTodos()
    } catch { /* ignore */ }
    finally { setAdding(false) }
  }

  const handleToggle = async (todo: Todo) => {
    try {
      await toggleTodo(todo.id)
      setTodos(prev =>
        prev.map(t => t.id === todo.id ? { ...t, completed: !t.completed } : t)
      )
    } catch { /* ignore */ }
  }


  const move = async (index: number, direction: number) => {
    const newIndex = index + direction
    if (newIndex < 0 || newIndex >= todos.length) return
    const reordered = [...todos]
    const [item] = reordered.splice(index, 1)
    reordered.splice(newIndex, 0, item)
    const original = todos
    setTodos(reordered)
    try {
      await reorderTodos(reordered.map(t => t.id))
    } catch {
      // Rollback to original order on failure
      setTodos(original)
    }
  }

  if (loading) {
    return (
      <div className="panel-section">
        <h3>{t('todo.title')}</h3>
        <div className="skeleton skeleton-text" style={{ height: 36, marginBottom: 8 }} />
        <div className="skeleton skeleton-text" style={{ height: 36, marginBottom: 8 }} />
        <div className="skeleton skeleton-text" style={{ height: 36 }} />
      </div>
    )
  }

  return (
    <div className="panel-section">
      <h3>{t('todo.title')} ({todos.filter(x => !x.completed).length}/{todos.length})</h3>

      {error && <ErrorBanner message={error} onRetry={fetchTodos} />}

      <div className={s.todoAdd}>
        <input
          type="text"
          value={title}
          onChange={e => setTitle(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') handleAdd() }}
          placeholder={t('todo.addPlaceholder')}
          className="form-input"
        />
        <button className="btn btn-primary btn-sm" onClick={handleAdd} disabled={adding || !title.trim()}>
          {t('todo.add')}
        </button>
      </div>

      {todos.length === 0 ? (
        <p className={`empty-hint ${s.emptyState}`}>{t('todo.empty')}</p>
      ) : (
        <ul className={s.todoList}>
          {todos.map((todo, i) => (
            <li key={todo.id} className={`${s.todoItem} ${todo.completed ? s.completed : ''}`}>
              <input
                type="checkbox"
                checked={todo.completed}
                onChange={() => handleToggle(todo)}
                className={s.todoCheckbox}
              />
              <span className={s.todoTitle}>{todo.title}</span>
              <div className={s.todoActions}>
                <button className="btn-icon" onClick={() => move(i, -1)} disabled={i === 0} title={t('todo.moveUp')} aria-label={t('todo.moveUp')}>
                  <Icon name="arrow-up" size={14} />
                </button>
                <button className="btn-icon" onClick={() => move(i, 1)} disabled={i === todos.length - 1} title={t('todo.moveDown')} aria-label={t('todo.moveDown')}>
                  <Icon name="arrow-down" size={14} />
                </button>
                <TodoDeleteButton todoId={todo.id} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export default TodoSection

/** Two-click confirm delete button for one todo item. */
function TodoDeleteButton({ todoId }: { todoId: number }) {
  const { t } = useTranslation()
  const { armed, click } = useConfirmClick(async () => {
    try {
      await deleteTodo(todoId)
      window.dispatchEvent(new CustomEvent('repotory:todos-changed'))
    } catch { /* ignore */ }
  })
  return (
    <button
      className={`btn-icon ${armed ? 'btn-delete-confirm' : 'btn-delete'}`}
      onClick={click}
      title={armed ? t('common.confirmDelete') : t('common.delete')}
    >
      {armed ? <Icon name="warning" size={14} /> : <Icon name="close" size={14} />}
    </button>
  )
}
