#include <jni.h>
#include <portaudio.h>
#include <stdlib.h>
#include <string.h>

static JavaVM* jvm = NULL;
static jobject gCallback = NULL;
static jmethodID gOnFrame = NULL;
static PaStream* captureStream = NULL;
static PaStream* playStream = NULL;

static int audioCallback(const void* input, void* output,
                          unsigned long frameCount,
                          const PaStreamCallbackTimeInfo* timeInfo,
                          PaStreamCallbackFlags flags, void* userData) {
    if (!input || !gCallback) return paContinue;
    JNIEnv* env;
    if ((*jvm)->AttachCurrentThread(jvm, (void**)&env, NULL) != JNI_OK) return paContinue;

    jfloatArray arr = (*env)->NewFloatArray(env, (jsize)frameCount);
    (*env)->SetFloatArrayRegion(env, arr, 0, (jsize)frameCount, (const float*)input);
    (*env)->CallVoidMethod(env, gCallback, gOnFrame, arr);
    (*env)->DeleteLocalRef(env, arr);
    return paContinue;
}

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_PortAudioJNI_start(
        JNIEnv* env, jclass cls, jobject callback, jint sampleRate, jint framesPerBuffer) {
    (*env)->GetJavaVM(env, &jvm);
    gCallback = (*env)->NewGlobalRef(env, callback);
    jclass cbClass = (*env)->GetObjectClass(env, gCallback);
    gOnFrame = (*env)->GetMethodID(env, cbClass, "onFrame", "([F)V");

    Pa_Initialize();
    Pa_OpenDefaultStream(&captureStream, 1, 0, paFloat32,
                         sampleRate, framesPerBuffer, audioCallback, NULL);
    Pa_StartStream(captureStream);
    return 0;
}

JNIEXPORT void JNICALL Java_ai_freeman_macos_audio_PortAudioJNI_stop(
        JNIEnv* env, jclass cls) {
    if (captureStream) {
        Pa_StopStream(captureStream);
        Pa_CloseStream(captureStream);
        captureStream = NULL;
    }
    if (gCallback) { (*env)->DeleteGlobalRef(env, gCallback); gCallback = NULL; }
    Pa_Terminate();
}

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_PortAudioJNI_playSamples(
        JNIEnv* env, jclass cls, jfloatArray samples, jint sampleRate) {
    jsize len = (*env)->GetArrayLength(env, samples);
    jfloat* data = (*env)->GetFloatArrayElements(env, samples, NULL);

    if (!playStream) {
        Pa_Initialize();
        Pa_OpenDefaultStream(&playStream, 0, 1, paFloat32, sampleRate, 512, NULL, NULL);
        Pa_StartStream(playStream);
    }
    Pa_WriteStream(playStream, data, len);
    (*env)->ReleaseFloatArrayElements(env, samples, data, JNI_ABORT);
    return 0;
}

JNIEXPORT void JNICALL Java_ai_freeman_macos_audio_PortAudioJNI_stopPlayback(
        JNIEnv* env, jclass cls) {
    if (playStream) {
        Pa_StopStream(playStream);
        Pa_CloseStream(playStream);
        playStream = NULL;
    }
}
