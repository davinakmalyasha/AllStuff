import { type HTMLAttributes, type ReactNode } from 'react'

interface CardProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactNode
  hover?: boolean
}

export function Card({ children, hover = false, className = '', ...rest }: CardProps) {
  return (
    <div
      className={[
        'card p-5',
        hover
          ? 'transition-shadow duration-200 hover:shadow-cardHover'
          : 'shadow-card',
        className,
      ].join(' ')}
      {...rest}
    >
      {children}
    </div>
  )
}
