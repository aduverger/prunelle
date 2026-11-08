import ReactMarkdown from 'react-markdown';

export function Markdown({ children }: { children: string }) {
  return (
    <div className="markdown">
      <ReactMarkdown
        skipHtml
        components={{
          a: (props) => (
            <a href={props.href} target="_blank" rel="noreferrer">
              {props.children}
            </a>
          ),
          img: () => <span className="muted">External image omitted.</span>,
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}
