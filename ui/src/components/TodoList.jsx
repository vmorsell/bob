export function TodoList({ todos }) {
  if (!todos || !todos.length) return null;
  return (
    <div class="cc-todos">
      {todos.map((t, i) => {
        const status = t.status || "pending";
        const icon =
          status === "completed" ? "\u2713" : status === "in_progress" ? "\u25cf" : "\u25cb";
        return (
          <div key={i} class={"cc-todo todo-" + status}>
            <span class="todo-i">{icon}</span>
            <span class="todo-t">{t.content || ""}</span>
          </div>
        );
      })}
    </div>
  );
}
