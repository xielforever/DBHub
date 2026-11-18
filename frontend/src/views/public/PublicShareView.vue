<script setup lang="ts">
import { FileX2 } from 'lucide-vue-next'
import BrandLogo from '../../components/base/BrandLogo.vue'
</script>

<template>
  <div class="editorial-shell min-h-screen flex flex-col">
    <header class="editorial-masthead">
      <BrandLogo />
    </header>
    <main class="grid flex-1 place-items-center px-4 py-10 sm:px-6" aria-labelledby="share-disabled-title">
      <section class="w-full max-w-lg text-center">
        <FileX2 class="mx-auto mb-5 size-12 text-accent" aria-hidden="true" />
        <h1 id="share-disabled-title" class="page-header__title">当前分享不可用</h1>
        <p class="mt-4 text-sm leading-relaxed text-muted">公开分享已停用。请登录后通过获准的工作空间访问内容。</p>
        <RouterLink to="/login" class="liquid-button ui-control-target inline-flex mt-6 items-center justify-center">返回登录</RouterLink>
      </section>
    </main>
  </div>
</template>