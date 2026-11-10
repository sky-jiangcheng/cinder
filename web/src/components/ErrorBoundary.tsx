import { Component, type ErrorInfo, type ReactNode } from 'react'
import Icon from './Icon'
import i18n from '../i18n'
import s from './ErrorBoundary.module.css'

interface Props {
  children: ReactNode
  fallback?: ReactNode
  onError?: (error: Error, info: ErrorInfo) => void
  /** When resetKey changes, any active error state is cleared automatically. */
  resetKey?: string
}

interface State {
  hasError: boolean
  error: Error | null
}

/**
 * ErrorBoundary catches rendering errors anywhere in the subtree and shows a
 * fallback UI instead of letting the entire app white-screen. Uncaught errors
 * are reported to the optional onError callback (e.g. for logging).
 *
 * Usage:
 *   <ErrorBoundary>
 *     <Dashboard />
 *   </ErrorBoundary>
 */
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidUpdate(prevProps: Props) {
    if (prevProps.resetKey !== this.props.resetKey && this.state.hasError) {
      this.setState({ hasError: false, error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.props.onError?.(error, info)
  }

  private handleReset = () => {
    this.setState({ hasError: false, error: null })
    window.location.hash = '#/'
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) return this.props.fallback
      return (
        <div className={`${s.errorBoundaryFallback}`}>
          <div className={`${s.errorBoundaryIcon}`}><Icon name="warning" size={34} /></div>
          <h2>{i18n.t('common.errorTitle')}</h2>
          <p className={`${s.errorBoundaryMessage}`}>{this.state.error?.message}</p>
          <button className="btn btn-primary" onClick={this.handleReset}>
            {i18n.t('common.retry')}
          </button>
        </div>
      )
    }
    return this.props.children
  }
}
