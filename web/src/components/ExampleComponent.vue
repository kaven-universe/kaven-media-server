<template>
    <div>
        <p>{{ title }}</p>
        <ul>
            <li v-for="todo in todos" :key="todo.id" @click="increment">
                {{ todo.id }} - {{ todo.content }}
            </li>
        </ul>
        <p>Count: {{ todoCount }} / {{ meta.totalCount }}</p>
        <p>Active: {{ active ? 'yes' : 'no' }}</p>
        <p>Clicks on todos: {{ clickCount }}</p>
    </div>
</template>

<script setup lang="ts">
import { ref, computed, toRef } from "vue";
import type { Meta, Todo } from "./models";

function useClickCount() {
    const clickCount = ref(0);
    function increment() {
        clickCount.value += 1;
        return clickCount.value;
    }
    return { clickCount, increment };
}

function useDisplayTodo(todos: { value: Todo[] }) {
    const todoCount = computed(() => todos.value.length);
    return { todoCount };
}

const props = withDefaults(defineProps<{
    title: string;
    todos?: Todo[];
    meta: Meta;
    active?: boolean;
}>(), {
    todos: () => [],
});

const { clickCount, increment } = useClickCount();
const { todoCount } = useDisplayTodo(toRef(props, "todos"));
</script>
